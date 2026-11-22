package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// EventIntake records one intake decision by the triage loop. Data holds
// {outcome, reason, actor, action}; action is admit, label_pending, refuse or
// regate.
const EventIntake = "intake"

var hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Admission is the gate's provenance for one issue: who admitted it, when,
// and the hash of the title and body that were admitted. A later edit changes
// the hash and re-gates the issue.
type Admission struct {
	Issue int
	// Mode is the intake mode when admitted: all or label.
	Mode string
	// Reason is the intake.Reason that admitted it.
	Reason string
	// LabeledBy is the login of the judged labeled event; "" in all mode.
	LabeledBy string
	// LabeledAt is on GitHub's clock and never zero: the judged labeled
	// event's time, or the issue's creation time in all mode.
	LabeledAt  time.Time
	BodyHash   string
	AdmittedAt time.Time // controller clock, informational
	JobID      string    // the lead job claimed on this admission
	// RegatedAt is the issue's updated_at (GitHub clock) when the edit that
	// changed the hash was first seen and the issue was re-gated; zero when
	// not re-gated. A re-admitting relabel must come after it.
	RegatedAt time.Time
	// RegatedHash is the body hash RegatedAt was recorded for.
	RegatedHash string
}

// Admission returns the admission of issue, or ErrNotFound.
func (s *Store) Admission(ctx context.Context, issue int) (Admission, error) {
	var a Admission
	var by, job, rhash sql.NullString
	var labeled, admitted int64
	var regated sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT issue, mode, reason, labeled_by, labeled_at, body_hash, admitted_at, job_id, regated_at, regated_hash
		FROM admissions WHERE issue = ?`, issue).Scan(&a.Issue, &a.Mode, &a.Reason, &by, &labeled, &a.BodyHash, &admitted, &job, &regated, &rhash)
	if errors.Is(err, sql.ErrNoRows) {
		return Admission{}, fmt.Errorf("admission of issue %d: %w", issue, ErrNotFound)
	}
	if err != nil {
		return Admission{}, fmt.Errorf("read admission of issue %d: %w", issue, err)
	}
	a.LabeledBy, a.JobID, a.RegatedHash = by.String, job.String, rhash.String
	if regated.Valid {
		a.RegatedAt = time.Unix(regated.Int64, 0).UTC()
	}
	a.LabeledAt, a.AdmittedAt = time.Unix(labeled, 0).UTC(), time.Unix(admitted, 0).UTC()
	return a, nil
}

// SetAdmission inserts or replaces the admission of a.Issue.
func (s *Store) SetAdmission(ctx context.Context, a Admission) error {
	switch {
	case a.Issue <= 0:
		return fmt.Errorf("set admission: issue must be positive, got %d", a.Issue)
	case a.Mode == "" || a.Reason == "":
		return fmt.Errorf("set admission of issue %d: mode and reason are required", a.Issue)
	case !hashRE.MatchString(a.BodyHash):
		return fmt.Errorf("set admission of issue %d: body hash must be 64 hex digits", a.Issue)
	case a.LabeledAt.IsZero():
		return fmt.Errorf("set admission of issue %d: labeled_at is required", a.Issue)
	}
	at := a.AdmittedAt
	if at.IsZero() {
		at = s.now()
	}
	var by, job, regated, rhash any
	if !a.RegatedAt.IsZero() {
		regated = a.RegatedAt.Unix()
	}
	if a.RegatedHash != "" {
		rhash = a.RegatedHash
	}
	if a.LabeledBy != "" {
		by = a.LabeledBy
	}
	if a.JobID != "" {
		job = a.JobID
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO admissions(issue, mode, reason, labeled_by, labeled_at, body_hash, admitted_at, job_id, regated_at, regated_hash)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(issue) DO UPDATE SET mode=excluded.mode, reason=excluded.reason, labeled_by=excluded.labeled_by,
			labeled_at=excluded.labeled_at, body_hash=excluded.body_hash, admitted_at=excluded.admitted_at, job_id=excluded.job_id,
			regated_at=excluded.regated_at, regated_hash=excluded.regated_hash`,
		a.Issue, a.Mode, a.Reason, by, a.LabeledAt.Unix(), a.BodyHash, at.Unix(), job, regated, rhash); err != nil {
		return fmt.Errorf("set admission of issue %d: %w", a.Issue, err)
	}
	return nil
}
