package controller

import (
	"context"
	"log/slog"
	"time"

	"github.com/albertcbrown/ard/internal/config"
)

const (
	// ReaperInterval is how often the Reaper runs. The proposal fixes it at
	// one minute; it is not configurable.
	ReaperInterval = time.Minute
	// MinInterval is the scheduler floor for any Interval result.
	MinInterval = time.Minute
)

// Loop is one polling loop. The controller owns scheduling, jitter, backoff,
// the kill switch and shutdown; a Loop only does one poll per Tick.
type Loop interface {
	// Name is unique across loops: "scanners", "triage", "workers",
	// "reviewers", "merger", "reaper", ...
	Name() string
	// Interval is read before every wait, so reloaded config applies.
	Interval(cfg *config.Resolved) time.Duration
	// Claims reports whether the loop starts work or writes to GitHub (claims,
	// scanner filing and autoclose, stale closes, merges); such loops are
	// skipped while the controller is paused. The Reaper and the costs loop (bookkeeping about work already done) return false.
	Claims() bool
	// Tick runs one poll. It must return promptly once ctx is done.
	Tick(ctx context.Context, env Env) error
}

// TickTimeouter is optionally implemented by a Loop that needs a per-tick
// deadline other than its interval (for example a scanner whose 4h interval
// is much longer than a sane run). The result is clamped to at least one
// second; a result <= 0 means the default, the loop's interval.
type TickTimeouter interface {
	TickTimeout(cfg *config.Resolved) time.Duration
}

// Env is what the controller passes to one Tick.
type Env struct {
	Log      *slog.Logger     // tagged loop=<name> tick=<n>
	Cfg      *config.Resolved // snapshot for this tick; read-only
	Inflight *Inflight
	// Stopping is closed when shutdown begins; long ticks may stop early.
	Stopping <-chan struct{}
}
