// Package digest posts the daily status-issue digest: queue depth by label,
// jobs run, failures and spend for the previous UTC day.
package digest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

// Name is the digest loop's name, as --loops spells it.
const Name = "digest"

// Store is the part of *state.Store the loop uses.
type Store interface {
	AppendEvent(ctx context.Context, e state.Event) error
	Events(ctx context.Context, f state.EventFilter) ([]state.Event, error)
	CountEndedSince(ctx context.Context, since, until time.Time) (map[state.RoleKey]state.JobCounts, error)
	SpendBetween(ctx context.Context, from, to time.Time) (map[state.RoleKey]state.Spend, state.Spend, error)
}

// Reader lists issues for the queue depth.
type Reader interface {
	ListIssues(ctx context.Context, o github.ListIssuesOpts) ([]github.Issue, error)
}

// Effects is the part of effects.Effects the loop uses.
type Effects interface {
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
	DryRun() bool
}

// Deps are the loop's collaborators. StatusIssue returns 0 until the status
// issue is known. Now nil means time.Now; Log nil means slog.Default.
type Deps struct {
	Store       Store
	Reader      Reader
	Effects     Effects
	StatusIssue func() int
	Log         *slog.Logger
	Now         func() time.Time
}

// Loop posts one digest comment per UTC day. The digest event written after
// the comment is the marker that keeps a restart from posting it again.
type Loop struct {
	d  Deps
	mu sync.Mutex
}

// NewLoop builds the digest loop.
func NewLoop(d Deps) *Loop {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Loop{d: d}
}

// Name implements controller.Loop.
func (*Loop) Name() string { return Name }

// Interval implements controller.Loop.
func (*Loop) Interval(*config.Resolved) time.Duration { return 10 * time.Minute }

// Claims is false: the digest is bookkeeping and runs while paused.
func (*Loop) Claims() bool { return false }

var _ controller.Loop = (*Loop)(nil)

// Tick posts today's digest when it is not yet posted.
func (l *Loop) Tick(ctx context.Context, _ controller.Env) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	issue := l.d.StatusIssue()
	if issue == 0 {
		return nil
	}
	now := l.d.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	from := today.Add(-24 * time.Hour)
	day := from.Format("2006-01-02")

	evs, err := l.d.Store.Events(ctx, state.EventFilter{Kind: state.EventDigest, Since: today})
	if err != nil {
		return fmt.Errorf("digest: read events: %w", err)
	}
	for _, e := range evs {
		var d struct {
			Day string `json:"day"`
		}
		if json.Unmarshal(e.Data, &d) == nil && d.Day == day {
			return nil
		}
	}

	issues, err := l.d.Reader.ListIssues(ctx, github.ListIssuesOpts{State: github.StateOpen})
	if err != nil {
		return fmt.Errorf("digest: list issues: %w", err)
	}
	queue := map[string]int{}
	for _, is := range issues {
		if is.IsPR {
			continue
		}
		for _, lb := range is.Labels {
			if strings.HasPrefix(lb, labels.Prefix) {
				queue[lb]++
			}
		}
	}
	jobs, err := l.d.Store.CountEndedSince(ctx, from, today)
	if err != nil {
		return fmt.Errorf("digest: count jobs: %w", err)
	}
	perSpend, spend, err := l.d.Store.SpendBetween(ctx, from, today)
	if err != nil {
		return fmt.Errorf("digest: read spend: %w", err)
	}
	data := Data{Day: day, Queue: queue, Jobs: jobs, Spend: perSpend, Total: spend}
	if l.d.Effects.DryRun() {
		l.d.Log.Info("digest.notice", "day", day)
		return nil
	}
	if _, err := l.d.Effects.CreateComment(ctx, issue, Render(data)); err != nil {
		l.d.Log.Error("digest.post_failed", "day", day, "error", err)
		return nil
	}
	nJobs, nFail := data.counts()
	ev, _ := json.Marshal(map[string]any{
		"day": day, "jobs": nJobs, "failures": nFail, "spend_usd": spend.USD, "unpriced": spend.Unpriced,
	})
	if err := l.d.Store.AppendEvent(ctx, state.Event{At: l.d.Now(), Kind: state.EventDigest, Data: ev}); err != nil {
		l.d.Log.Error("digest.unrecorded", "day", day, "error", err)
	}
	return nil
}
