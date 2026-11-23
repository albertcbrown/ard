package state

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
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
		var models sql.NullString
		var ti, to, tw, tr sql.NullInt64
		var cost sql.NullFloat64
		if err := rows.Scan(&models, &ti, &to, &tw, &tr, &cost); err != nil {
			return JobUsage{}, fmt.Errorf("job usage %s: %w", jobID, err)
		}
		out.Steps++
		if models.Valid {
			for _, m := range strings.Split(models.String, ",") {
				if m != "" && !seen[m] {
					seen[m] = true
					out.Models = append(out.Models, m)
				}
			}
		}
		if ti.Valid || to.Valid || tw.Valid || tr.Valid {
			if out.Tokens == nil {
				out.Tokens = &Tokens{}
			}
			out.Tokens.Input += ti.Int64
			out.Tokens.Output += to.Int64
			out.Tokens.CacheWrite += tw.Int64
			out.Tokens.CacheRead += tr.Int64
		}
		if cost.Valid {
			if out.CostUSD == nil {
				out.CostUSD = new(float64)
			}
			*out.CostUSD += cost.Float64
		}
	}
	if err := rows.Err(); err != nil {
		return JobUsage{}, fmt.Errorf("job usage %s: %w", jobID, err)
	}
	sort.Strings(out.Models)
	return out, nil
}
