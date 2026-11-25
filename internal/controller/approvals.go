package controller

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

// ApprovalsName is the name of the approval watch loop.
const ApprovalsName = "approvals"

// Wake reasons logged as controller.wake reason=....
const (
	WakeCloseout  = "closeout"
	WakeApproval  = "approval"
	WakeUnblocked = "unblocked"
)

// ApprovalWatch is the controller's cheap poll for human hand-offs (#406): a
// person adding ard:approved, or removing ard:needs-human or
// ard:executor:failed. It runs no agent, takes no slot and only reads from
// GitHub; a hit wakes the loop that owns the item, and that loop's own gate
// does the real check (who approved, and after triage finished).
type ApprovalWatch struct {
	// Search is github.Reader.SearchIssues.
	Search func(ctx context.Context, q string, o github.SearchOpts) ([]github.Issue, error)
	// Wake is Controller.WakeLoops. A nil Wake makes the watch do nothing.
	Wake func(reason string, names ...string)
	// Paused, when non-nil and true, skips the poll (the kill switch).
	Paused func(ctx context.Context) bool

	mu      sync.Mutex
	started bool
	woken   map[int]time.Time // approved items already woken for, by UpdatedAt
	blocked map[int][]string  // items seen blocked -> loops that own them
}

// Name implements Loop.
func (*ApprovalWatch) Name() string { return ApprovalsName }

// Claims is false: the watch only reads, and checks the kill switch itself.
func (*ApprovalWatch) Claims() bool { return false }

// Interval implements Loop.
func (*ApprovalWatch) Interval(cfg *config.Resolved) time.Duration {
	return time.Duration(cfg.Intervals.Approvals)
}

// Tick implements Loop.
func (w *ApprovalWatch) Tick(ctx context.Context, env Env) error {
	if w.Search == nil || w.Wake == nil {
		return nil
	}
	if w.Paused != nil && w.Paused(ctx) {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.woken == nil {
		w.woken, w.blocked = map[int]time.Time{}, map[int][]string{}
	}
	opts := github.SearchOpts{Sort: "updated", Order: "desc", Max: 100}

	approved, err := w.Search(ctx, `is:open label:"`+labels.Approved+`"`, opts)
	if err != nil {
		return fmt.Errorf("approval watch: search approved: %w", err)
	}
	var hit bool
	seen := map[int]bool{}
	for _, it := range approved {
		seen[it.Number] = true
		if it.IsPR || hasStage(it.Labels, labels.StageWorker) {
			continue // the worker label is already there
		}
		if t, ok := w.woken[it.Number]; ok && t.Equal(it.UpdatedAt) {
			continue
		}
		w.woken[it.Number] = it.UpdatedAt
		hit = true
	}
	for n := range w.woken {
		if !seen[n] {
			delete(w.woken, n)
		}
	}
	if hit {
		w.Wake(WakeApproval, "triage")
	}

	blocked := map[int][]string{}
	for _, l := range []string{labels.NeedsHuman, labels.ExecutorFailed} {
		hits, err := w.Search(ctx, `is:open label:"`+l+`"`, opts)
		if err != nil {
			return fmt.Errorf("approval watch: search %s: %w", l, err)
		}
		for _, it := range hits {
			blocked[it.Number] = owners(it)
		}
	}
	// A label removal cannot be searched for, so an item seen blocked that
	// no longer is has been unblocked (or closed; a spare wake is harmless).
	// After a restart the first poll has nothing to compare with; the loops'
	// own ticks cover that gap.
	var wake []string
	if w.started {
		for n, own := range w.blocked {
			if _, still := blocked[n]; !still {
				wake = append(wake, own...)
			}
		}
	}
	w.blocked, w.started = blocked, true
	if len(wake) > 0 {
		w.Wake(WakeUnblocked, wake...)
	}
	return nil
}

func hasStage(ls []string, s labels.Stage) bool {
	for _, l := range ls {
		if p, ok := labels.Parse(l); ok && p.Kind == labels.KindRole && p.Stage == s {
			return true
		}
	}
	return false
}

// owners names the loops that act on an item, from its labels.
func owners(it github.Issue) []string {
	var out []string
	add := func(n string) {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	for _, l := range it.Labels {
		if l == labels.Pending {
			add("triage")
		}
		p, ok := labels.Parse(l)
		if !ok || p.Kind != labels.KindRole {
			continue
		}
		switch p.Stage {
		case labels.StageTriage:
			add("triage")
		case labels.StageWorker:
			add("workers")
		case labels.StageReview:
			add("reviewers")
			if p.State == labels.StateChanges {
				add("reviewfix")
			}
		}
	}
	if len(out) == 0 {
		if it.IsPR {
			add("reviewers")
		} else {
			add("triage")
		}
	}
	return out
}
