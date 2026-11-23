// Package costs is the costs Loop: it writes each issue's cost table (the
// ard-owned block in the issue body, see internal/costblock) from controller
// state. A close-out only marks the issue dirty; this loop does the GitHub
// write, so a failed or slow table write can never fail or delay a close-out.
//
// The table holds counts, dollars, model ids, outcomes and links only: every
// cell comes from state or from a fixed vocabulary, never from agent text,
// prompts, logs or host paths.
package costs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/costblock"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/state"
)

const (
	// Interval is how often the loop looks for dirty tables.
	Interval = time.Minute
	// batch is how many issues one tick writes.
	batch = 20
	// maxFailures is how many failed writes an issue gets before the loop
	// gives up until the next close-out on it re-marks it.
	maxFailures = 10
)

// Store is the part of state.Store the loop uses.
type Store interface {
	DirtyCostTables(ctx context.Context, limit int) ([]state.CostTable, error)
	CostTableDone(ctx context.Context, issue int, gen int64) error
	CostTableFailed(ctx context.Context, issue int) (int, error)
	ClosedJobsOf(ctx context.Context, issue int) ([]state.Job, error)
	IssueUsage(ctx context.Context, issue int) (map[string]state.JobUsage, error)
}

// Reader reads an issue (github.Reader).
type Reader interface {
	GetIssue(ctx context.Context, n int) (github.Issue, error)
}

// Effects is the subset of effects.Effects the loop uses: its only write.
type Effects interface {
	UpdateIssueBody(ctx context.Context, n int, body string) error
	DryRun() bool
}

// Deps are the loop's dependencies.
type Deps struct {
	Store   Store
	Reader  Reader
	Effects Effects
	Repo    github.Repo
	// Locks is shared with the scanner pipeline: one body writer per issue.
	Locks *costblock.Locks
	Log   *slog.Logger
	Now   func() time.Time
}

// Loop implements controller.Loop.
type Loop struct{ d Deps }

// New returns the costs loop.
func New(d Deps) *Loop { return &Loop{d: d} }

// Name implements controller.Loop.
func (*Loop) Name() string { return "costs" }

// Interval implements controller.Loop.
func (*Loop) Interval(*config.Resolved) time.Duration { return Interval }

// Claims is false: the table is bookkeeping about work already done, not new
// work, so it keeps being written while the controller is paused (like the
// Reaper). The kill switch stops claims, not records.
func (*Loop) Claims() bool { return false }

func (l *Loop) log() *slog.Logger {
	if l.d.Log != nil {
		return l.d.Log
	}
	return slog.Default()
}

// Tick writes up to batch dirty tables, one at a time (one writer per table).
// It returns an error only when the store itself fails, so one bad issue does
// not back the loop off.
func (l *Loop) Tick(ctx context.Context, _ controller.Env) error {
	dirty, err := l.d.Store.DirtyCostTables(ctx, batch)
	if err != nil {
		return fmt.Errorf("costs: %w", err)
	}
	for _, c := range dirty {
		if ctx.Err() != nil {
			return nil
		}
		if err := l.write(ctx, c); err != nil {
			return fmt.Errorf("costs: %w", err) // transient: back off
		}
	}
	return nil
}

// write brings issue c.Issue's table up to date. Every outcome is logged and
// ends in done, failed or "left dirty"; none returns an error.
func (l *Loop) write(ctx context.Context, c state.CostTable) error {
	n := c.Issue
	log := l.log().With("issue", n)
	unlock := l.d.Locks.Lock(n)
	defer unlock()

	// Render first: this is local and needs no GitHub.
	rows, err := l.rows(ctx, n, log)
	if err != nil {
		return l.failed(ctx, log, c, err)
	}
	block, folded, ok := costblock.Render(rows, costblock.MaxBlockBytes)
	if !ok {
		log.Warn("costs.nothing_to_render", "rows", len(rows))
		l.done(ctx, log, c)
		return nil
	}
	if costblock.Region(block) != block { // Render must never emit a block Valid rejects
		log.Error("costs.render_invalid", "bytes", len(block))
		l.done(ctx, log, c)
		return nil
	}
	if n < 0 { // a dry-run synthetic issue: there is no body to read
		log.Info("costs.table_dry", "rows", len(rows), "folded", folded, "bytes", len(block))
		l.done(ctx, log, c)
		return nil
	}

	body, err := l.d.Reader.GetIssue(ctx, n)
	if errors.Is(err, github.ErrNotFound) {
		log.Info("costs.issue_gone")
		l.done(ctx, log, c)
		return nil
	}
	if err != nil {
		return l.failed(ctx, log, c, fmt.Errorf("read issue: %w", err))
	}
	cur := body.Body
	for attempt := 0; ; attempt++ {
		next, blk, fit := fitBody(cur, rows, block)
		if !fit {
			log.Warn("costs.body_full", "body_runes", utf8.RuneCountInString(cur))
			l.done(ctx, log, c)
			return nil
		}
		if next == cur {
			log.Info("costs.table_unchanged", "rows", len(rows))
			l.done(ctx, log, c)
			return nil
		}
		if err := l.d.Effects.UpdateIssueBody(ctx, n, next); err != nil {
			return l.failed(ctx, log, c, fmt.Errorf("write body: %w", err))
		}
		dry := l.d.Effects.DryRun()
		if dry { // the write only reached the ledger: nothing to verify
			log.Info("costs.table_written", "rows", len(rows), "folded", folded, "bytes", len(blk), "dry_run", true)
			l.done(ctx, log, c)
			return nil
		}
		back, err := l.d.Reader.GetIssue(ctx, n)
		if err != nil {
			return l.failed(ctx, log, c, fmt.Errorf("verify: %w", err))
		}
		if norm(costblock.Region(back.Body)) == norm(blk) {
			log.Info("costs.table_written", "rows", len(rows), "folded", folded, "bytes", len(blk), "dry_run", false)
			l.done(ctx, log, c)
			return nil
		}
		// Another writer replaced the body after ours. Splice onto what is
		// there now (never onto older text) and write once more.
		log.Warn("costs.body_raced", "attempt", attempt+1)
		if attempt == 1 {
			return l.failed(ctx, log, c, errors.New("body changed under the write twice"))
		}
		cur = back.Body
	}
}

// fitBody splices block into body. When the result would pass GitHub's body
// limit it re-renders the block into what is left. fit is false when even a
// fully folded block does not fit.
func fitBody(body string, rows []costblock.Row, block string) (next, blk string, fit bool) {
	next = costblock.Splice(body, block)
	if utf8.RuneCountInString(next) <= costblock.MaxBodyRunes {
		return next, block, true
	}
	outside := utf8.RuneCountInString(next) - utf8.RuneCountInString(block)
	room := min(costblock.MaxBlockBytes, costblock.MaxBodyRunes-outside-2)
	small, _, ok := costblock.Render(rows, room)
	if !ok {
		return "", "", false
	}
	return costblock.Splice(body, small), small, true
}

func (l *Loop) done(ctx context.Context, log *slog.Logger, c state.CostTable) {
	if err := l.d.Store.CostTableDone(ctx, c.Issue, c.Gen); err != nil {
		log.Error("costs.mark_done_failed", "error", err)
	}
}

func (l *Loop) failed(ctx context.Context, log *slog.Logger, c state.CostTable, err error) error {
	n := c.Issue
	if github.IsTransient(err) {
		// A rate limit or outage is not this issue's fault: do not count it
		// toward giving up. Stop the tick so the loop backs off.
		log.Warn("costs.write_deferred", "error", err)
		return err
	}
	log.Warn("costs.write_failed", "error", err)
	failures, ferr := l.d.Store.CostTableFailed(ctx, n)
	if ferr != nil {
		log.Error("costs.mark_failed_failed", "error", ferr)
		return nil
	}
	if failures >= maxFailures {
		log.Error("costs.gave_up", "failures", failures)
		// The next close-out on the issue marks it dirty again.
		if derr := l.d.Store.CostTableDone(ctx, n, c.Gen); derr != nil {
			log.Error("costs.mark_done_failed", "error", derr)
		}
	}
	return nil
}

func norm(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }
