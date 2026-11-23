package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/albertcbrown/ard/internal/state/sqlitex"
)

// Event kinds.
const (
	EventClaim    = "claim"
	EventCloseout = "closeout"
	EventFailure  = "failure"
	EventRetry    = "retry"
	// EventUsageLimit: a job stopped at a usage limit. It is not a failure.
	EventUsageLimit = "usage_limit"
	// EventKill: the Reaper is about to kill a running job; data has the reason.
	EventKill      = "kill"
	EventTickError = "tick_error"
	// EventHostUntrusted: a host-mode job of a role that reads untrusted
	// input started. Stage, role, job and issue are in their columns.
	EventHostUntrusted = "host_untrusted"
	// EventGate: the triage fan-in gate acted; data has verdict, reason and worker.
	EventGate = "gate"
	// EventMerge: the merger squash-merged a PR; data has pr, head and merge_sha.
	EventMerge = "merge"
	// EventReviewRound: a review fix round changed phase; data has round, phase, reason, pr, head, worker and roles.
	EventReviewRound = "review_round"
	// EventReviewRerequest: the merger removed a stale :done review label so the
	// review re-runs; data has pr, head and reviewed. Stage is review and role
	// is the reviewer role. One per head: it stops the merger from looping.
	EventReviewRerequest = "review_rerequest"
	// EventBudgetCapped: the budget loop posted the status-issue notice for a
	// reached daily cap; data has scope ("repo" or "role"), stage, role, day
	// (2006-01-02, UTC), spent_usd, cap_usd and unpriced. It is also the
	// marker that stops a second notice for the same scope and day.
	EventBudgetCapped = "budget_capped"
)

// Event is one row of the append-only event log. Data holds structured fields
// only: never log text, issue bodies or secrets.
type Event struct {
	ID    int64
	At    time.Time
	Kind  string
	JobID string
	Issue int
	Stage string
	Role  string
	Data  json.RawMessage
}

// EventFilter selects events; zero values are unbounded.
type EventFilter struct {
	Since, Until time.Time
	Kind         string
	Issue        int    // 0 = any
	Stage, Role  string // "" = any
	Limit        int
}

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func insertEvent(ctx context.Context, x execer, e Event) error {
	if len(e.Data) > 0 && !json.Valid(e.Data) {
		return fmt.Errorf("event %s: data is not valid JSON", e.Kind)
	}
	var data any
	if len(e.Data) > 0 {
		data = string(e.Data)
	}
	var job, stage, role any
	if e.JobID != "" {
		job = e.JobID
	}
	if e.Stage != "" {
		stage = e.Stage
	}
	if e.Role != "" {
		role = e.Role
	}
	var issue any
	if e.Issue != 0 {
		issue = e.Issue
	}
	if _, err := x.ExecContext(ctx, `INSERT INTO events(ts, kind, job_id, issue, stage, role, data)
		VALUES(?,?,?,?,?,?,?)`, e.At.Unix(), e.Kind, job, issue, stage, role, data); err != nil {
		return fmt.Errorf("append event %s: %w", e.Kind, err)
	}
	return nil
}

// AppendEvent appends e; a zero At means now.
func (s *Store) AppendEvent(ctx context.Context, e Event) error {
	if e.Kind == "" {
		return fmt.Errorf("append event: empty kind")
	}
	if e.At.IsZero() {
		e.At = s.now()
	}
	return insertEvent(ctx, s.db, e)
}

// Events returns matching events in ascending id order.
func (s *Store) Events(ctx context.Context, f EventFilter) ([]Event, error) {
	q := `SELECT id, ts, kind, job_id, issue, stage, role, data FROM events WHERE 1=1`
	var args []any
	if !f.Since.IsZero() {
		q += ` AND ts >= ?`
		args = append(args, f.Since.Unix())
	}
	if !f.Until.IsZero() {
		q += ` AND ts <= ?`
		args = append(args, f.Until.Unix())
	}
	if f.Kind != "" {
		q += ` AND kind = ?`
		args = append(args, f.Kind)
	}
	if f.Issue != 0 {
		q += ` AND issue = ?`
		args = append(args, f.Issue)
	}
	if f.Stage != "" {
		q += ` AND stage = ?`
		args = append(args, f.Stage)
	}
	if f.Role != "" {
		q += ` AND role = ?`
		args = append(args, f.Role)
	}
	q += ` ORDER BY id`
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var ts int64
		var job, stage, role, data sql.NullString
		var issue sql.NullInt64
		if err := rows.Scan(&e.ID, &ts, &e.Kind, &job, &issue, &stage, &role, &data); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		e.At = sqlitex.FromUnix(sql.NullInt64{Int64: ts, Valid: true})
		e.JobID, e.Stage, e.Role = job.String, stage.String, role.String
		e.Issue = int(issue.Int64)
		if data.Valid {
			e.Data = json.RawMessage(data.String)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	return out, nil
}
