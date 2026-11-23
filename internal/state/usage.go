package state

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Tokens are token counts by type.
type Tokens struct{ Input, Output, CacheWrite, CacheRead int64 }

// StepUsage is what one agent run reported.
type StepUsage struct {
	Models  []string // validated model ids; nil when none
	Tokens  *Tokens  // nil: not reported
	CostUSD *float64 // nil: not reported
}

// JobUsage is a job's usage summed over its steps.
type JobUsage struct {
	Models  []string // union, sorted
	Tokens  *Tokens  // nil when no step reported tokens
	CostUSD *float64 // nil when no step reported a cost
	Steps   int
}

var (
	stepRE  = regexp.MustCompile(`^[0-9A-Za-z]{1,64}$`)
	modelRE = regexp.MustCompile(`^[A-Za-z0-9._:\[\]/-]{1,100}$`)
)

const maxModels = 8

// ValidModel reports whether s may be stored as a model id.
func ValidModel(s string) bool { return modelRE.MatchString(s) }

// RecordStepUsage stores one step once: a second call for the same job and step
// is a no-op that returns recorded=false. In the same transaction it sets
// jobs.cost_usd to the sum of the job's step costs (NULL when none), so a
// failed, retried or usage-limited job keeps its cost.
func (s *Store) RecordStepUsage(ctx context.Context, jobID, step string, u StepUsage) (bool, error) {
	if !stepRE.MatchString(step) {
		return false, fmt.Errorf("record step usage %s: invalid step %q", jobID, step)
	}
	if t := u.Tokens; t != nil && (t.Input < 0 || t.Output < 0 || t.CacheWrite < 0 || t.CacheRead < 0) {
		return false, fmt.Errorf("record step usage %s: negative token count", jobID)
	}
	if c := u.CostUSD; c != nil && (math.IsNaN(*c) || math.IsInf(*c, 0) || *c < 0) {
		return false, fmt.Errorf("record step usage %s: invalid cost", jobID)
	}
	seen := map[string]bool{}
	var models []string
	for _, m := range u.Models {
		if !modelRE.MatchString(m) || seen[m] {
			continue
		}
		seen[m] = true
		models = append(models, m)
	}
	sort.Strings(models)
	if len(models) > maxModels {
		models = models[:maxModels]
	}
	var mv, ti, to, tw, tr, cv any
	if len(models) > 0 {
		mv = strings.Join(models, ",")
	}
	if t := u.Tokens; t != nil {
		ti, to, tw, tr = t.Input, t.Output, t.CacheWrite, t.CacheRead
	}
	if u.CostUSD != nil {
		cv = *u.CostUSD
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("record step usage %s: begin: %w", jobID, err)
	}
	defer tx.Rollback() //nolint:errcheck
	res, err := tx.ExecContext(ctx, `INSERT INTO step_usage(job_id, step, models, tokens_in, tokens_out,
		tokens_cache_write, tokens_cache_read, cost_usd, recorded_at) VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(job_id, step) DO NOTHING`, jobID, step, mv, ti, to, tw, tr, cv, s.now().Unix())
	if err != nil {
		return false, fmt.Errorf("record step usage %s: %w", jobID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("record step usage %s: %w", jobID, err)
	}
	if n == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET cost_usd = (SELECT SUM(cost_usd) FROM step_usage WHERE job_id = ?) WHERE id = ?`, jobID, jobID); err != nil {
		return false, fmt.Errorf("record step usage %s: job cost: %w", jobID, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("record step usage %s: commit: %w", jobID, err)
	}
	return true, nil
}

// usageRow is one step_usage row.
type usageRow struct {
	models         sql.NullString
	ti, to, tw, tr sql.NullInt64
	cost           sql.NullFloat64
}

// add folds one step into u. seen de-duplicates u's models.
func (u *JobUsage) add(r usageRow, seen map[string]bool) {
	u.Steps++
	if r.models.Valid {
		for _, m := range strings.Split(r.models.String, ",") {
			if m != "" && !seen[m] {
				seen[m] = true
				u.Models = append(u.Models, m)
			}
		}
	}
	if r.ti.Valid || r.to.Valid || r.tw.Valid || r.tr.Valid {
		if u.Tokens == nil {
			u.Tokens = &Tokens{}
		}
		u.Tokens.Input += r.ti.Int64
		u.Tokens.Output += r.to.Int64
		u.Tokens.CacheWrite += r.tw.Int64
		u.Tokens.CacheRead += r.tr.Int64
	}
	if r.cost.Valid {
		if u.CostUSD == nil {
			u.CostUSD = new(float64)
		}
		*u.CostUSD += r.cost.Float64
	}
}

// JobUsage sums a job's recorded steps. A job with none gives the zero value.
func (s *Store) JobUsage(ctx context.Context, jobID string) (JobUsage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT models, tokens_in, tokens_out, tokens_cache_write, tokens_cache_read, cost_usd
		FROM step_usage WHERE job_id = ? ORDER BY step`, jobID)
	if err != nil {
		return JobUsage{}, fmt.Errorf("job usage %s: %w", jobID, err)
	}
	defer rows.Close()
	var out JobUsage
	seen := map[string]bool{}
	for rows.Next() {
		var r usageRow
		if err := rows.Scan(&r.models, &r.ti, &r.to, &r.tw, &r.tr, &r.cost); err != nil {
			return JobUsage{}, fmt.Errorf("job usage %s: %w", jobID, err)
		}
		out.add(r, seen)
	}
	if err := rows.Err(); err != nil {
		return JobUsage{}, fmt.Errorf("job usage %s: %w", jobID, err)
	}
	sort.Strings(out.Models)
	return out, nil
}

// IssueUsage sums the recorded steps of every non-scanner job of an issue, by
// job id, in one query. A job with no steps is absent.
func (s *Store) IssueUsage(ctx context.Context, issue int) (map[string]JobUsage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.job_id, u.models, u.tokens_in, u.tokens_out, u.tokens_cache_write, u.tokens_cache_read, u.cost_usd
		FROM step_usage u JOIN jobs j ON j.id = u.job_id WHERE j.issue = ? AND j.stage != 'scanner' ORDER BY u.job_id, u.step`, issue)
	if err != nil {
		return nil, fmt.Errorf("issue usage %d: %w", issue, err)
	}
	defer rows.Close()
	out := map[string]JobUsage{}
	seen := map[string]map[string]bool{}
	for rows.Next() {
		var id string
		var r usageRow
		if err := rows.Scan(&id, &r.models, &r.ti, &r.to, &r.tw, &r.tr, &r.cost); err != nil {
			return nil, fmt.Errorf("issue usage %d: %w", issue, err)
		}
		if seen[id] == nil {
			seen[id] = map[string]bool{}
		}
		u := out[id]
		u.add(r, seen[id])
		out[id] = u
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("issue usage %d: %w", issue, err)
	}
	for id, u := range out {
		sort.Strings(u.Models)
		out[id] = u
	}
	return out, nil
}

// Spend is money spent over a period: USD sums the priced steps; Unpriced
// counts steps that reported no cost (they add nothing to USD).
type Spend struct {
	USD      float64
	Unpriced int
}

// RoleKey names a role in spend totals. The stage is part of the key because
// a role name can be shared by a triage role and a worker role.
type RoleKey struct{ Stage, Role string }

// SpendSince sums step usage recorded at or after dayStart, per (stage, role)
// and for the whole repo.
func (s *Store) SpendSince(ctx context.Context, dayStart time.Time) (map[RoleKey]Spend, Spend, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT j.stage, j.role, COALESCE(SUM(u.cost_usd), 0), COALESCE(SUM(u.cost_usd IS NULL), 0)
		FROM step_usage u JOIN jobs j ON j.id = u.job_id
		WHERE u.recorded_at >= ? GROUP BY j.stage, j.role`, dayStart.Unix())
	if err != nil {
		return nil, Spend{}, fmt.Errorf("spend since %s: %w", dayStart.UTC().Format(time.RFC3339), err)
	}
	defer rows.Close()
	per := map[RoleKey]Spend{}
	var repo Spend
	for rows.Next() {
		var k RoleKey
		var sp Spend
		if err := rows.Scan(&k.Stage, &k.Role, &sp.USD, &sp.Unpriced); err != nil {
			return nil, Spend{}, fmt.Errorf("spend since: %w", err)
		}
		per[k] = sp
		repo.USD += sp.USD
		repo.Unpriced += sp.Unpriced
	}
	if err := rows.Err(); err != nil {
		return nil, Spend{}, fmt.Errorf("spend since: %w", err)
	}
	return per, repo, nil
}
