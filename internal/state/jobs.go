package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/albertcbrown/ard/internal/state/sqlitex"
)

// Status is a job's lifecycle state.
type Status string

const (
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// roleRE mirrors the role-name pattern of the config package (#11); keep in sync.
var roleRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

var stages = map[string]bool{"scanner": true, "triage": true, "worker": true, "review": true}

// Job is one claimed unit of work.
type Job struct {
	ID            string
	Stage, Role   string
	Issue, PR     int // Issue is 0 only for stage scanner; PR 0 means none
	Status        Status
	Attempt       int
	Handle        json.RawMessage // opaque executor handle (#18)
	CloseoutPhase string
	CloseoutPlan  json.RawMessage
	FailReason    string
	KeepUntil     time.Time
	CleanedAt     time.Time
	CostUSD       *float64
	CreatedAt     time.Time
	UpdatedAt     time.Time
	EndedAt       time.Time
}

// NewJob describes a claim. Data goes on the claim event.
type NewJob struct {
	Stage, Role string
	Issue, PR   int
	Data        json.RawMessage
}

const jobCols = `id, stage, role, issue, pr, status, attempt, handle, closeout_phase, closeout_plan,
	fail_reason, keep_until, cleaned_at, cost_usd, created_at, updated_at, ended_at`

type scanner interface{ Scan(dest ...any) error }

func scanJob(r scanner) (Job, error) {
	var j Job
	var pr, keep, cleaned, ended sql.NullInt64
	var handle, phase, plan, reason sql.NullString
	var cost sql.NullFloat64
	var created, updated int64
	if err := r.Scan(&j.ID, &j.Stage, &j.Role, &j.Issue, &pr, &j.Status, &j.Attempt, &handle, &phase, &plan,
		&reason, &keep, &cleaned, &cost, &created, &updated, &ended); err != nil {
		return Job{}, err
	}
	j.PR = int(pr.Int64)
	if handle.Valid {
		j.Handle = json.RawMessage(handle.String)
	}
	j.CloseoutPhase = phase.String
	if plan.Valid {
		j.CloseoutPlan = json.RawMessage(plan.String)
	}
	j.FailReason = reason.String
	j.KeepUntil, j.CleanedAt, j.EndedAt = sqlitex.FromUnix(keep), sqlitex.FromUnix(cleaned), sqlitex.FromUnix(ended)
	if cost.Valid {
		c := cost.Float64
		j.CostUSD = &c
	}
	j.CreatedAt = time.Unix(created, 0).UTC()
	j.UpdatedAt = time.Unix(updated, 0).UTC()
	return j, nil
}

func (n NewJob) validate() error {
	if !stages[n.Stage] {
		return fmt.Errorf("invalid stage %q", n.Stage)
	}
	if !roleRE.MatchString(n.Role) {
		return fmt.Errorf("invalid role %q", n.Role)
	}
	if n.Stage == "scanner" && n.Issue != 0 {
		return fmt.Errorf("scanner job must have issue 0, got %d", n.Issue)
	}
	if n.Stage != "scanner" && n.Issue <= 0 {
		return fmt.Errorf("%s job needs a positive issue, got %d", n.Stage, n.Issue)
	}
	if len(n.Data) > 0 && !json.Valid(n.Data) {
		return fmt.Errorf("claim data is not valid JSON")
	}
	return nil
}

// CreateJob is the claim record: in one transaction it mints the ULID,
// inserts a running job with attempt 0 and appends a "claim" event.
func (s *Store) CreateJob(ctx context.Context, n NewJob) (Job, error) {
	if err := n.validate(); err != nil {
		return Job{}, fmt.Errorf("create job: %w", err)
	}
	id, err := s.newID()
	if err != nil {
		return Job{}, err
	}
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, fmt.Errorf("create job: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	var pr any
	if n.PR != 0 {
		pr = n.PR
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO jobs(id, stage, role, issue, pr, status, attempt, created_at, updated_at)
		VALUES(?,?,?,?,?,'running',0,?,?)`, id, n.Stage, n.Role, n.Issue, pr, now.Unix(), now.Unix()); err != nil {
		if isUnique(err) {
			return Job{}, fmt.Errorf("claim %s/%s issue %d: %w", n.Stage, n.Role, n.Issue, ErrConflict)
		}
		return Job{}, fmt.Errorf("claim %s/%s issue %d: %w", n.Stage, n.Role, n.Issue, err)
	}
	if err := insertEvent(ctx, tx, Event{At: now, Kind: EventClaim, JobID: id, Issue: n.Issue, Stage: n.Stage, Role: n.Role, Data: n.Data}); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, fmt.Errorf("claim job %s: commit: %w", id, err)
	}
	return s.Job(ctx, id)
}

// Job returns the job with id, or ErrNotFound.
func (s *Store) Job(ctx context.Context, id string) (Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fmt.Errorf("job %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Job{}, fmt.Errorf("read job %s: %w", id, err)
	}
	return j, nil
}

// RunningJob returns the running job for stage, role and issue, or ErrNotFound.
func (s *Store) RunningJob(ctx context.Context, stage, role string, issue int) (Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs
		WHERE stage = ? AND role = ? AND issue = ? AND status = 'running'`, stage, role, issue))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fmt.Errorf("running job %s/%s issue %d: %w", stage, role, issue, ErrNotFound)
	}
	if err != nil {
		return Job{}, fmt.Errorf("read running job %s/%s issue %d: %w", stage, role, issue, err)
	}
	return j, nil
}

// RunningJobs returns all running jobs ordered by created_at, id.
func (s *Store) RunningJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE status = 'running' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list running jobs: %w", err)
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan running job: %w", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list running jobs: %w", err)
	}
	return out, nil
}

func (s *Store) update(ctx context.Context, op, id, set string, args ...any) error {
	args = append(append([]any{}, args...), s.now().Unix(), id)
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET `+set+`, updated_at = ? WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("%s %s: %w", op, id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s %s: %w", op, id, ErrNotFound)
	}
	return nil
}

func nullJSON(b json.RawMessage) (any, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("not valid JSON")
	}
	return string(b), nil
}

// SetHandle stores the opaque executor handle.
func (s *Store) SetHandle(ctx context.Context, id string, h json.RawMessage) error {
	v, err := nullJSON(h)
	if err != nil {
		return fmt.Errorf("set handle %s: %w", id, err)
	}
	return s.update(ctx, "set handle", id, `handle = ?`, v)
}

// SetCloseout records the close-out phase; a nil plan keeps the stored one.
func (s *Store) SetCloseout(ctx context.Context, id, phase string, plan json.RawMessage) error {
	v, err := nullJSON(plan)
	if err != nil {
		return fmt.Errorf("set closeout %s: %w", id, err)
	}
	return s.update(ctx, "set closeout", id, `closeout_phase = ?, closeout_plan = COALESCE(?, closeout_plan)`, phase, v)
}

// OpenCloseouts returns the jobs whose close-out has started but not finished
// (phase set and not "closed"), oldest first. serve startup and the Reaper
// resume them.
func (s *Store) OpenCloseouts(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobCols+` FROM jobs
		WHERE closeout_phase IS NOT NULL AND closeout_phase NOT IN ('', 'closed') ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list open closeouts: %w", err)
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan open closeout: %w", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list open closeouts: %w", err)
	}
	return out, nil
}

// CloseoutsOf returns the jobs of an issue and stage that have a stored
// close-out record, oldest first. The worker loop reads the reviewers the lead
// deferred from them.
func (s *Store) CloseoutsOf(ctx context.Context, issue int, stage string) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobCols+` FROM jobs
		WHERE issue = ? AND stage = ? AND closeout_plan IS NOT NULL ORDER BY created_at, id`, issue, stage)
	if err != nil {
		return nil, fmt.Errorf("list closeouts of issue %d: %w", issue, err)
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan closeout: %w", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list closeouts of issue %d: %w", issue, err)
	}
	return out, nil
}

// Attempt is one execution attempt of a job.
type Attempt struct {
	JobID     string
	N         int
	StartedAt time.Time
	EndedAt   time.Time
	ExitCode  *int
	Cause     string
	Transient bool
	CostUSD   *float64
}

// AttemptEnd records how an attempt finished.
type AttemptEnd struct {
	ExitCode  *int
	Cause     string
	Transient bool
	CostUSD   *float64
}

// StartAttempt increments jobs.attempt and inserts the attempt row.
func (s *Store) StartAttempt(ctx context.Context, id string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("start attempt %s: begin: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck
	now := s.now().Unix()
	var n int
	err = tx.QueryRowContext(ctx, `UPDATE jobs SET attempt = attempt + 1, updated_at = ?
		WHERE id = ? AND status = 'running' RETURNING attempt`, now, id).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		var st string
		if e := tx.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = ?`, id).Scan(&st); errors.Is(e, sql.ErrNoRows) {
			return 0, fmt.Errorf("start attempt %s: %w", id, ErrNotFound)
		}
		return 0, fmt.Errorf("start attempt %s: %w", id, ErrNotRunning)
	}
	if err != nil {
		return 0, fmt.Errorf("start attempt %s: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO attempts(job_id, n, started_at) VALUES(?,?,?)`, id, n, now); err != nil {
		return 0, fmt.Errorf("start attempt %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("start attempt %s: commit: %w", id, err)
	}
	return n, nil
}

// EndAttempt records the outcome of attempt n; ErrNotFound if it does not exist.
func (s *Store) EndAttempt(ctx context.Context, id string, n int, e AttemptEnd) error {
	var code, cause, cost any
	if e.ExitCode != nil {
		code = *e.ExitCode
	}
	if e.Cause != "" {
		cause = e.Cause
	}
	if e.CostUSD != nil {
		cost = *e.CostUSD
	}
	res, err := s.db.ExecContext(ctx, `UPDATE attempts SET ended_at = ?, exit_code = ?, cause = ?, transient = ?, cost_usd = ?
		WHERE job_id = ? AND n = ?`, s.now().Unix(), code, cause, e.Transient, cost, id, n)
	if err != nil {
		return fmt.Errorf("end attempt %s#%d: %w", id, n, err)
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return fmt.Errorf("end attempt %s#%d: %w", id, n, ErrNotFound)
	}
	return nil
}

// Attempts returns the attempts of a job in order.
func (s *Store) Attempts(ctx context.Context, id string) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT n, started_at, ended_at, exit_code, cause, transient, cost_usd
		FROM attempts WHERE job_id = ? ORDER BY n`, id)
	if err != nil {
		return nil, fmt.Errorf("list attempts %s: %w", id, err)
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		a := Attempt{JobID: id}
		var started int64
		var ended, code sql.NullInt64
		var cause sql.NullString
		var cost sql.NullFloat64
		if err := rows.Scan(&a.N, &started, &ended, &code, &cause, &a.Transient, &cost); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		a.StartedAt = time.Unix(started, 0).UTC()
		a.EndedAt = sqlitex.FromUnix(ended)
		if code.Valid {
			c := int(code.Int64)
			a.ExitCode = &c
		}
		a.Cause = cause.String
		if cost.Valid {
			c := cost.Float64
			a.CostUSD = &c
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list attempts %s: %w", id, err)
	}
	return out, nil
}

// finish moves a running job to a terminal status with its event. Repeating the
// same transition is a no-op; any other non-running status is ErrNotRunning.
func (s *Store) finish(ctx context.Context, op, id string, to Status, kind, reason string, keep time.Time, cost *float64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%s %s: begin: %w", op, id, err)
	}
	defer tx.Rollback() //nolint:errcheck
	j, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s %s: %w", op, id, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w", op, id, err)
	}
	if j.Status == to {
		return nil
	}
	if j.Status != StatusRunning {
		return fmt.Errorf("%s %s: %w", op, id, ErrNotRunning)
	}
	now := s.now()
	var c, r any
	if cost != nil {
		c = *cost
	}
	if reason != "" {
		r = reason
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status = ?, cost_usd = COALESCE(?, cost_usd), fail_reason = ?,
		keep_until = ?, ended_at = ?, updated_at = ? WHERE id = ?`,
		string(to), c, r, sqlitex.ToUnix(keep), now.Unix(), now.Unix(), id); err != nil {
		return fmt.Errorf("%s %s: %w", op, id, err)
	}
	var data json.RawMessage
	if reason != "" {
		data, _ = json.Marshal(map[string]string{"reason": reason})
	}
	if err := insertEvent(ctx, tx, Event{At: now, Kind: kind, JobID: id, Issue: j.Issue, Stage: j.Stage, Role: j.Role, Data: data}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s %s: commit: %w", op, id, err)
	}
	return nil
}

// Complete marks the job done and appends a "closeout" event.
func (s *Store) Complete(ctx context.Context, id string, costUSD *float64) error {
	return s.finish(ctx, "complete job", id, StatusDone, EventCloseout, "", time.Time{}, costUSD)
}

// Fail marks the job failed, records keep_until and appends a "failure" event.
func (s *Store) Fail(ctx context.Context, id, reason string, keepUntil time.Time) error {
	return s.finish(ctx, "fail job", id, StatusFailed, EventFailure, reason, keepUntil, nil)
}
