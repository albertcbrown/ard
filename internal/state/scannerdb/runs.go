package scannerdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/albertcbrown/ard/internal/state/sqlitex"
)

// Run is one scanner execution.
type Run struct {
	ID                    int64
	StartedAt, FinishedAt time.Time
	SHA, Outcome          string
	Found, Filed, Closed  int
}

// RecordRun inserts r and returns its id. Outcome must be ok, dropped, timeout or error.
func (d *DB) RecordRun(ctx context.Context, r Run) (int64, error) {
	if r.StartedAt.IsZero() {
		return 0, fmt.Errorf("scanner %s record run: started_at is required", d.name)
	}
	var sha any
	if r.SHA != "" {
		sha = r.SHA
	}
	res, err := d.db.ExecContext(ctx, `INSERT INTO runs(started_at, finished_at, sha, outcome, found, filed, closed)
		VALUES(?,?,?,?,?,?,?)`, r.StartedAt.Unix(), sqlitex.ToUnix(r.FinishedAt), sha, r.Outcome, r.Found, r.Filed, r.Closed)
	if err != nil {
		return 0, fmt.Errorf("scanner %s record run: %w", d.name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("scanner %s record run: %w", d.name, err)
	}
	return id, nil
}

// LastRun returns the run with the latest started_at (then id), or ErrNotFound.
func (d *DB) LastRun(ctx context.Context) (Run, error) {
	var r Run
	var started int64
	var fin sql.NullInt64
	var sha sql.NullString
	err := d.db.QueryRowContext(ctx, `SELECT id, started_at, finished_at, sha, outcome, found, filed, closed
		FROM runs ORDER BY started_at DESC, id DESC LIMIT 1`).Scan(&r.ID, &started, &fin, &sha, &r.Outcome, &r.Found, &r.Filed, &r.Closed)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, fmt.Errorf("scanner %s last run: %w", d.name, ErrNotFound)
	}
	if err != nil {
		return Run{}, fmt.Errorf("scanner %s last run: %w", d.name, err)
	}
	r.StartedAt, r.FinishedAt, r.SHA = time.Unix(started, 0).UTC(), sqlitex.FromUnix(fin), sha.String
	return r, nil
}
