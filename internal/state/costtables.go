package state

import (
	"context"
	"fmt"
	"time"
)

// CostTable is the dirty flag of one issue's cost table in its body.
type CostTable struct {
	Issue    int
	MarkedAt time.Time
	Failures int
}

// markCostTable runs inside the SetCloseout transaction: a close-out that
// reaches "closed" leaves its issue dirty, and a replay re-marks harmlessly.
const markCostTable = `INSERT INTO cost_tables(issue, dirty, marked_at)
	SELECT issue, 1, ? FROM jobs WHERE id = ? AND stage IN ('triage','worker','review')
	ON CONFLICT(issue) DO UPDATE SET dirty = 1, marked_at = excluded.marked_at, failures = 0`

// DirtyCostTables returns up to limit issues whose table needs a write, oldest
// mark first.
func (s *Store) DirtyCostTables(ctx context.Context, limit int) ([]CostTable, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT issue, marked_at, failures FROM cost_tables
		WHERE dirty = 1 ORDER BY marked_at, issue LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("dirty cost tables: %w", err)
	}
	defer rows.Close()
	var out []CostTable
	for rows.Next() {
		var c CostTable
		var at int64
		if err := rows.Scan(&c.Issue, &at, &c.Failures); err != nil {
			return nil, fmt.Errorf("dirty cost tables: %w", err)
		}
		c.MarkedAt = time.Unix(at, 0).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// CostTableDone clears the flag only when it is still the mark the writer saw,
// so a close-out that marks during a write is not lost.
func (s *Store) CostTableDone(ctx context.Context, issue int, markedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE cost_tables SET dirty = 0, written_at = ?, failures = 0
		WHERE issue = ? AND marked_at = ?`, s.now().Unix(), issue, markedAt.Unix())
	if err != nil {
		return fmt.Errorf("cost table done %d: %w", issue, err)
	}
	return nil
}

// CostTableFailed counts one failed write and returns the running count.
func (s *Store) CostTableFailed(ctx context.Context, issue int) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `UPDATE cost_tables SET failures = failures + 1 WHERE issue = ? RETURNING failures`, issue).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("cost table failed %d: %w", issue, err)
	}
	return n, nil
}

// ClosedJobsOf returns the non-scanner jobs of an issue whose close-out is
// closed, oldest first. Each gets a row in the cost table.
func (s *Store) ClosedJobsOf(ctx context.Context, issue int) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobCols+` FROM jobs
		WHERE issue = ? AND stage != 'scanner' AND closeout_phase = 'closed' ORDER BY created_at, id`, issue)
	if err != nil {
		return nil, fmt.Errorf("closed jobs of %d: %w", issue, err)
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("closed jobs of %d: %w", issue, err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
