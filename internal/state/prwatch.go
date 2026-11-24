package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PRWatch is what the prwatch loop remembers about one pull request.
type PRWatch struct {
	PR    int
	Issue int
	Head  string
	// ChecksState is "green", "pending" or "red" for Head, "" when unknown.
	ChecksState string
	HeadSeenAt  time.Time
	// PendingNotifiedHead and RedNotifiedHead are the heads a notice was sent for.
	PendingNotifiedHead string
	RedNotifiedHead     string
	ClosedNotified      bool
	MergeRecorded       bool
}

// GetPRWatch returns the row of pr; ok is false when there is none.
func (s *Store) GetPRWatch(ctx context.Context, pr int) (w PRWatch, ok bool, err error) {
	var seen int64
	var closed, merged int
	err = s.db.QueryRowContext(ctx, `SELECT pr, issue, head, checks_state, head_seen_at,
		pending_notified_head, red_notified_head, closed_notified, merge_recorded
		FROM pr_watch WHERE pr = ?`, pr).Scan(&w.PR, &w.Issue, &w.Head, &w.ChecksState, &seen,
		&w.PendingNotifiedHead, &w.RedNotifiedHead, &closed, &merged)
	if errors.Is(err, sql.ErrNoRows) {
		return PRWatch{}, false, nil
	}
	if err != nil {
		return PRWatch{}, false, fmt.Errorf("get pr_watch %d: %w", pr, err)
	}
	w.HeadSeenAt = time.Unix(seen, 0).UTC()
	w.ClosedNotified, w.MergeRecorded = closed != 0, merged != 0
	return w, true, nil
}

// PutPRWatch inserts or replaces the row of w.PR.
func (s *Store) PutPRWatch(ctx context.Context, w PRWatch) error {
	b := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO pr_watch(pr, issue, head, checks_state, head_seen_at,
		pending_notified_head, red_notified_head, closed_notified, merge_recorded)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(pr) DO UPDATE SET issue = excluded.issue, head = excluded.head,
		checks_state = excluded.checks_state, head_seen_at = excluded.head_seen_at,
		pending_notified_head = excluded.pending_notified_head, red_notified_head = excluded.red_notified_head,
		closed_notified = excluded.closed_notified, merge_recorded = excluded.merge_recorded`,
		w.PR, w.Issue, w.Head, w.ChecksState, w.HeadSeenAt.Unix(),
		w.PendingNotifiedHead, w.RedNotifiedHead, b(w.ClosedNotified), b(w.MergeRecorded))
	if err != nil {
		return fmt.Errorf("put pr_watch %d: %w", w.PR, err)
	}
	return nil
}

// DeletePRWatch removes the row of pr.
func (s *Store) DeletePRWatch(ctx context.Context, pr int) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM pr_watch WHERE pr = ?`, pr); err != nil {
		return fmt.Errorf("delete pr_watch %d: %w", pr, err)
	}
	return nil
}
