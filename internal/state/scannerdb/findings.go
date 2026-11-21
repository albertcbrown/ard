package scannerdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Status of a finding. Fixed is a tombstone of a completed close.
type Status string

const (
	StatusOpen  Status = "open"
	StatusFixed Status = "fixed"
)

// Finding maps a fingerprint to the issue filed for it. Issue may be negative:
// a dry-run synthetic number.
type Finding struct {
	Fingerprint string
	Issue       int
	Status      Status
	Severity    string
	Group       string
	FirstSeen   time.Time
	LastSeen    time.Time
	LastSHA     string
}

// Suppression hides a fingerprint until Until. Expired rows are kept and
// returned; the caller compares Until.
type Suppression struct {
	Fingerprint string
	Until       time.Time
	Severity    string
	Issue       int
	Reason      string
}

// Batch is applied in one transaction: deletes first, then puts. PutFindings
// upserts and keeps the stored first_seen when the row exists.
type Batch struct {
	DeleteFindings     []string
	DeleteSuppressions []string
	PutFindings        []Finding
	PutSuppressions    []Suppression
}

const findingCols = `fingerprint, issue, status, severity, group_key, first_seen, last_seen, last_sha`

func scanFinding(r interface{ Scan(...any) error }) (Finding, error) {
	var f Finding
	var first, last int64
	var sha sql.NullString
	if err := r.Scan(&f.Fingerprint, &f.Issue, &f.Status, &f.Severity, &f.Group, &first, &last, &sha); err != nil {
		return Finding{}, err
	}
	f.FirstSeen, f.LastSeen, f.LastSHA = time.Unix(first, 0).UTC(), time.Unix(last, 0).UTC(), sha.String
	return f, nil
}

func (d *DB) queryFindings(ctx context.Context, where string, args ...any) ([]Finding, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+findingCols+` FROM findings `+where+` ORDER BY fingerprint`, args...)
	if err != nil {
		return nil, fmt.Errorf("scanner %s list findings: %w", d.name, err)
	}
	defer rows.Close()
	var out []Finding
	for rows.Next() {
		f, err := scanFinding(rows)
		if err != nil {
			return nil, fmt.Errorf("scanner %s scan finding: %w", d.name, err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scanner %s list findings: %w", d.name, err)
	}
	return out, nil
}

// Findings returns all findings ordered by fingerprint.
func (d *DB) Findings(ctx context.Context) ([]Finding, error) { return d.queryFindings(ctx, "") }

// FindingsForIssue returns the findings filed under issue.
func (d *DB) FindingsForIssue(ctx context.Context, issue int) ([]Finding, error) {
	return d.queryFindings(ctx, `WHERE issue = ?`, issue)
}

// Finding returns one finding, or ErrNotFound.
func (d *DB) Finding(ctx context.Context, fp string) (Finding, error) {
	f, err := scanFinding(d.db.QueryRowContext(ctx, `SELECT `+findingCols+` FROM findings WHERE fingerprint = ?`, fp))
	if errors.Is(err, sql.ErrNoRows) {
		return Finding{}, fmt.Errorf("scanner %s finding %s: %w", d.name, fp, ErrNotFound)
	}
	if err != nil {
		return Finding{}, fmt.Errorf("scanner %s read finding %s: %w", d.name, fp, err)
	}
	return f, nil
}

func scanSuppression(r interface{ Scan(...any) error }) (Suppression, error) {
	var s Suppression
	var until int64
	var issue sql.NullInt64
	var reason sql.NullString
	if err := r.Scan(&s.Fingerprint, &until, &s.Severity, &issue, &reason); err != nil {
		return Suppression{}, err
	}
	s.Until, s.Issue, s.Reason = time.Unix(until, 0).UTC(), int(issue.Int64), reason.String
	return s, nil
}

// Suppressions returns all suppressions ordered by fingerprint, expired included.
func (d *DB) Suppressions(ctx context.Context) ([]Suppression, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT fingerprint, until, severity, issue, reason FROM suppressions ORDER BY fingerprint`)
	if err != nil {
		return nil, fmt.Errorf("scanner %s list suppressions: %w", d.name, err)
	}
	defer rows.Close()
	var out []Suppression
	for rows.Next() {
		s, err := scanSuppression(rows)
		if err != nil {
			return nil, fmt.Errorf("scanner %s scan suppression: %w", d.name, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scanner %s list suppressions: %w", d.name, err)
	}
	return out, nil
}

// Suppression returns one suppression, or ErrNotFound. Expired rows are returned.
func (d *DB) Suppression(ctx context.Context, fp string) (Suppression, error) {
	s, err := scanSuppression(d.db.QueryRowContext(ctx,
		`SELECT fingerprint, until, severity, issue, reason FROM suppressions WHERE fingerprint = ?`, fp))
	if errors.Is(err, sql.ErrNoRows) {
		return Suppression{}, fmt.Errorf("scanner %s suppression %s: %w", d.name, fp, ErrNotFound)
	}
	if err != nil {
		return Suppression{}, fmt.Errorf("scanner %s read suppression %s: %w", d.name, fp, err)
	}
	return s, nil
}

func validateFinding(f Finding) error {
	if f.Fingerprint == "" {
		return errors.New("finding with empty fingerprint")
	}
	if f.Status != StatusOpen && f.Status != StatusFixed {
		return fmt.Errorf("finding %s: unknown status %q", f.Fingerprint, f.Status)
	}
	if f.FirstSeen.IsZero() || f.LastSeen.IsZero() {
		return fmt.Errorf("finding %s: first_seen and last_seen are required", f.Fingerprint)
	}
	return nil
}

func validateSuppression(s Suppression) error {
	if s.Fingerprint == "" {
		return errors.New("suppression with empty fingerprint")
	}
	if s.Until.IsZero() {
		return fmt.Errorf("suppression %s: until is required", s.Fingerprint)
	}
	return nil
}

func (b Batch) validate() error {
	for _, f := range b.PutFindings {
		if err := validateFinding(f); err != nil {
			return err
		}
	}
	for _, s := range b.PutSuppressions {
		if err := validateSuppression(s); err != nil {
			return err
		}
	}
	return nil
}

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func putFinding(ctx context.Context, x execer, f Finding) error {
	var sha any
	if f.LastSHA != "" {
		sha = f.LastSHA
	}
	_, err := x.ExecContext(ctx, `INSERT INTO findings(`+findingCols+`) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(fingerprint) DO UPDATE SET issue = excluded.issue, status = excluded.status,
		severity = excluded.severity, group_key = excluded.group_key,
		last_seen = excluded.last_seen, last_sha = excluded.last_sha`,
		f.Fingerprint, f.Issue, string(f.Status), f.Severity, f.Group, f.FirstSeen.Unix(), f.LastSeen.Unix(), sha)
	return err
}

func putSuppression(ctx context.Context, x execer, s Suppression) error {
	var issue, reason any
	if s.Issue != 0 {
		issue = s.Issue
	}
	if s.Reason != "" {
		reason = s.Reason
	}
	_, err := x.ExecContext(ctx, `INSERT INTO suppressions(fingerprint, until, severity, issue, reason) VALUES(?,?,?,?,?)
		ON CONFLICT(fingerprint) DO UPDATE SET until = excluded.until, severity = excluded.severity,
		issue = excluded.issue, reason = excluded.reason`,
		s.Fingerprint, s.Until.Unix(), s.Severity, issue, reason)
	return err
}

func (d *DB) tx(ctx context.Context, op string, fn func(*sql.Tx) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("scanner %s %s: begin: %w", d.name, op, err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := fn(tx); err != nil {
		return fmt.Errorf("scanner %s %s: %w", d.name, op, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("scanner %s %s: commit: %w", d.name, op, err)
	}
	return nil
}

func applyBatch(ctx context.Context, tx *sql.Tx, b Batch) error {
	for _, fp := range b.DeleteFindings {
		if _, err := tx.ExecContext(ctx, `DELETE FROM findings WHERE fingerprint = ?`, fp); err != nil {
			return err
		}
	}
	for _, fp := range b.DeleteSuppressions {
		if _, err := tx.ExecContext(ctx, `DELETE FROM suppressions WHERE fingerprint = ?`, fp); err != nil {
			return err
		}
	}
	for _, f := range b.PutFindings {
		if err := putFinding(ctx, tx, f); err != nil {
			return fmt.Errorf("put finding %s: %w", f.Fingerprint, err)
		}
	}
	for _, s := range b.PutSuppressions {
		if err := putSuppression(ctx, tx, s); err != nil {
			return fmt.Errorf("put suppression %s: %w", s.Fingerprint, err)
		}
	}
	return nil
}

// Apply applies b atomically. Any invalid row rejects the whole batch.
func (d *DB) Apply(ctx context.Context, b Batch) error {
	if err := b.validate(); err != nil {
		return fmt.Errorf("scanner %s apply: %w", d.name, err)
	}
	return d.tx(ctx, "apply", func(tx *sql.Tx) error { return applyBatch(ctx, tx, b) })
}

// Replace wipes findings and suppressions and inserts the given rows in one
// transaction (rebuild-state). Runs and meta are untouched.
func (d *DB) Replace(ctx context.Context, f []Finding, s []Suppression) error {
	b := Batch{PutFindings: f, PutSuppressions: s}
	if err := b.validate(); err != nil {
		return fmt.Errorf("scanner %s replace: %w", d.name, err)
	}
	return d.tx(ctx, "replace", func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM findings`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM suppressions`); err != nil {
			return err
		}
		return applyBatch(ctx, tx, b)
	})
}
