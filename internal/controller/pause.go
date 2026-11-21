package controller

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
)

// LabelPause is the label on the status issue that stops new claims. It is
// the catalogue name from the proposal; #22's labels package should own this
// constant once it lands.
const LabelPause = "ard:pause"

// Reason says why the controller is paused.
type Reason string

// Pause reasons.
const (
	ReasonNone          Reason = ""
	ReasonConfig        Reason = "config"         // cfg.Paused
	ReasonConfigInvalid Reason = "config-invalid" // reload failed (set by the scheduler)
	ReasonLabel         Reason = "label"          // ard:pause on the status issue
	ReasonUnreadable    Reason = "status-unreadable"
)

// PauseState is the result of a kill-switch check.
type PauseState struct {
	Paused bool
	Reason Reason
	Err    error // set for ReasonUnreadable
}

// Pauser decides whether claiming loops may tick.
type Pauser interface {
	Check(ctx context.Context, cfg *config.Resolved) PauseState
}

// IssueGetter is the one GitHub read the gate needs.
type IssueGetter interface {
	GetIssue(ctx context.Context, n int) (github.Issue, error)
}

// PauseGate checks config and the status issue's labels. It fails closed: an
// unreadable status issue counts as paused. It is safe for concurrent use.
type PauseGate struct {
	gh  IssueGetter
	log *slog.Logger

	mu          sync.Mutex
	issue       int
	unreadable  bool // last check failed; for once-per-transition logging
	warnedClose bool
}

// NewPauseGate builds a gate with no status issue; until SetStatusIssue is
// called with a positive number only config pauses.
func NewPauseGate(gh IssueGetter, log *slog.Logger) *PauseGate {
	if log == nil {
		log = slog.Default()
	}
	return &PauseGate{gh: gh, log: log}
}

// SetStatusIssue sets the status issue number. n <= 0 (unset, or a dry-run
// synthetic number) means config only.
func (g *PauseGate) SetStatusIssue(n int) {
	g.mu.Lock()
	g.issue = n
	g.unreadable, g.warnedClose = false, false
	g.mu.Unlock()
}

// Check implements Pauser. It makes no cache: one GetIssue per call.
func (g *PauseGate) Check(ctx context.Context, cfg *config.Resolved) PauseState {
	if cfg != nil && cfg.Paused {
		return PauseState{Paused: true, Reason: ReasonConfig}
	}
	g.mu.Lock()
	n := g.issue
	g.mu.Unlock()
	if n <= 0 {
		return PauseState{}
	}
	is, err := g.gh.GetIssue(ctx, n)
	if err != nil {
		g.mu.Lock()
		first := !g.unreadable
		g.unreadable = true
		g.mu.Unlock()
		if first {
			if errors.Is(err, github.ErrNotFound) {
				g.log.Warn("pause.status_issue_unreadable", "issue", n, "error", err,
					"hint", "status issue is gone; restart ard serve to recreate it")
			} else {
				g.log.Warn("pause.status_issue_unreadable", "issue", n, "error", err)
			}
		}
		return PauseState{Paused: true, Reason: ReasonUnreadable, Err: err}
	}
	g.mu.Lock()
	if g.unreadable {
		g.log.Info("pause.status_issue_readable", "issue", n)
	}
	g.unreadable = false
	warn := is.State == github.StateClosed && !g.warnedClose
	if warn {
		g.warnedClose = true
	}
	g.mu.Unlock()
	if warn {
		g.log.Warn("pause.status_issue_closed", "issue", n)
	}
	if slices.Contains(is.Labels, LabelPause) {
		return PauseState{Paused: true, Reason: ReasonLabel}
	}
	return PauseState{}
}
