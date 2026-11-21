package stale

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/labels"
)

const day = 24 * time.Hour

var (
	t0    = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	quiet = slog.New(slog.NewTextHandler(io.Discard, nil))
	bot   = "ard-test[bot]"
)

func cfg() *config.Resolved {
	return &config.Resolved{
		Roles: []config.Role{{Name: "cve", Kind: config.KindScanner}},
		Stale: config.Stale{InactiveFor: config.Duration(30 * day), SuppressFor: config.Duration(90 * day)},
	}
}

func in() inputs {
	return inputs{bot: bot, scanners: map[string]bool{"cve": true}, inactiveFor: 30 * day, now: t0}
}

func old(extra ...string) github.Issue {
	return github.Issue{
		Title: "x", State: github.StateOpen, Author: bot, CreatedAt: t0.Add(-40 * day),
		Labels: append([]string{labels.Scanner("cve"), labels.Pending}, extra...),
	}
}

func newEnv(t *testing.T) (*fake.Fake, *Closer) {
	t.Helper()
	f := fake.New(fake.WithClock(func() time.Time { return t0 }))
	return f, New(f, effects.NewLive(f, nil, quiet), func() time.Time { return t0 })
}

func run(c *Closer) error { return c.run(context.Background(), quiet, cfg()) }

func closedState(t *testing.T, f *fake.Fake, n int) github.Issue {
	t.Helper()
	is, err := f.GetIssue(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	return is
}

func TestLoopShape(t *testing.T) {
	c := New(nil, nil, nil)
	if c.Name() != "stale" || c.Interval(cfg()) != 24*time.Hour || !c.Claims() {
		t.Errorf("name %q interval %v claims %v", c.Name(), c.Interval(cfg()), c.Claims())
	}
	var _ controller.Loop = c
}

func TestRunClosesEligibleIssuesOnly(t *testing.T) {
	f, c := newEnv(t)
	a := f.SeedIssue(old())
	b := f.SeedIssue(old())
	human := old()
	human.Author = "alice"
	hn := f.SeedIssue(human)
	hold := f.SeedIssue(old(labels.Hold))
	runn := f.SeedIssue(old(labels.Running(labels.StageWorker, "engineer")))
	fresh := old()
	fresh.CreatedAt = t0.Add(-1 * day)
	fn := f.SeedIssue(fresh)
	f.SeedTimeline(b, github.TimelineEvent{Event: "commented", Actor: "bob", CreatedAt: t0.Add(-40*day + time.Hour)})

	if err := run(c); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{a, b, hn, hold, runn, fn} {
		is := closedState(t, f, n)
		want := n == a || n == b
		if (is.State == github.StateClosed) != want {
			t.Errorf("#%d state %s, closed want %v", n, is.State, want)
		}
		if want && is.StateReason != github.ReasonNotPlanned {
			t.Errorf("#%d reason %s", n, is.StateReason)
		}
	}
	var kinds []string
	for _, w := range f.Writes() {
		kinds = append(kinds, w.Method)
	}
	if got := strings.Join(kinds, ","); got != "CreateComment,UpdateIssue,CreateComment,UpdateIssue" {
		t.Errorf("writes = %s", got)
	}
	body := f.Writes()[0].Args[1].(string)
	for _, want := range []string{Marker, "30d", "90d"} {
		if !strings.Contains(body, want) {
			t.Errorf("comment lacks %q: %s", want, body)
		}
	}
	// A second run closes nothing and writes nothing.
	n := len(f.Writes())
	if err := run(c); err != nil || len(f.Writes()) != n {
		t.Errorf("second run: err %v, %d new writes", err, len(f.Writes())-n)
	}
}

func TestRunPriorCommentNotRepeated(t *testing.T) {
	f, c := newEnv(t)
	n := f.SeedIssue(old())
	f.SeedTimeline(n, github.TimelineEvent{Event: "commented", Actor: bot, Body: Marker + "\nold", CreatedAt: t0.Add(-time.Hour)})
	if err := run(c); err != nil {
		t.Fatal(err)
	}
	if w := f.Writes(); len(w) != 1 || w[0].Method != "UpdateIssue" {
		t.Errorf("writes = %v", w)
	}
}

func TestRunReopenedGetsFreshComment(t *testing.T) {
	f, c := newEnv(t)
	n := f.SeedIssue(old())
	f.SeedTimeline(n, github.TimelineEvent{Event: "commented", Actor: bot, Body: Marker, CreatedAt: t0.Add(-35 * day)})
	f.SeedTimeline(n, github.TimelineEvent{Event: "reopened", Actor: "alice", CreatedAt: t0.Add(-31 * day)})
	if err := run(c); err != nil {
		t.Fatal(err)
	}
	if w := f.Writes(); len(w) != 2 || w[0].Method != "CreateComment" {
		t.Errorf("writes = %v", w)
	}
}

func TestRunNoBotIdentity(t *testing.T) {
	tests := []struct {
		name string
		id   github.Identity
		fail error
	}{
		{"token auth", github.Identity{Login: "alice", Kind: github.AuthToken}, nil},
		{"empty login", github.Identity{Kind: github.AuthApp}, nil},
		{"identity error", github.Identity{Login: bot, Kind: github.AuthApp}, errors.New("boom")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := fake.New(fake.WithIdentity(tc.id), fake.WithClock(func() time.Time { return t0 }))
			f.SeedIssue(old())
			if tc.fail != nil {
				f.FailNext("Identity", tc.fail)
			}
			err := run(New(f, effects.NewLive(f, nil, quiet), func() time.Time { return t0 }))
			if !errors.Is(err, ErrNoBotIdentity) || len(f.Writes()) != 0 {
				t.Errorf("err %v, writes %v", err, f.Writes())
			}
		})
	}
}

func TestRunErrors(t *testing.T) {
	t.Run("search", func(t *testing.T) {
		f, c := newEnv(t)
		f.SeedIssue(old())
		f.FailNext("SearchIssues", errors.New("boom"))
		if err := run(c); err == nil || len(f.Writes()) != 0 {
			t.Errorf("err %v writes %v", err, f.Writes())
		}
	})
	t.Run("timeline error on one issue", func(t *testing.T) {
		f, c := newEnv(t)
		a, b := f.SeedIssue(old()), f.SeedIssue(old())
		f.FailNext("ListTimeline", errors.New("boom"))
		err := run(c)
		if err == nil || !strings.Contains(err.Error(), "#1") {
			t.Errorf("err = %v", err)
		}
		if closedState(t, f, a).State != github.StateOpen || closedState(t, f, b).State != github.StateClosed {
			t.Error("A should stay open and B close")
		}
	})
	t.Run("comment error leaves issue open", func(t *testing.T) {
		f, c := newEnv(t)
		n := f.SeedIssue(old())
		f.FailNext("CreateComment", errors.New("boom"))
		if err := run(c); err == nil || closedState(t, f, n).State != github.StateOpen {
			t.Errorf("err %v", err)
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		f, c := newEnv(t)
		f.SeedIssue(old())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := c.run(ctx, quiet, cfg()); err == nil || len(f.Writes()) != 0 {
			t.Errorf("err %v writes %v", err, f.Writes())
		}
	})
}

// racer adds a :running label between the search and the re-fetch.
type racer struct {
	*fake.Fake
	label string
}

func (r racer) GetIssue(ctx context.Context, n int) (github.Issue, error) {
	_ = r.Fake.AddLabels(ctx, n, r.label)
	return r.Fake.GetIssue(ctx, n)
}

func TestRunRaceWithClaim(t *testing.T) {
	f := fake.New(fake.WithClock(func() time.Time { return t0 }))
	f.SeedIssue(old())
	c := New(racer{f, labels.Running(labels.StageWorker, "engineer")}, effects.NewLive(f, nil, quiet), func() time.Time { return t0 })
	if err := run(c); err != nil {
		t.Fatal(err)
	}
	for _, w := range f.Writes() {
		if w.Method != "AddLabels" {
			t.Errorf("unexpected write %v", w)
		}
	}
}

func TestRunDryRunWritesNothing(t *testing.T) {
	f := fake.New(fake.WithClock(func() time.Time { return t0 }))
	n := f.SeedIssue(old())
	dr, err := effects.NewDryRun(effects.DryRunOptions{Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	if err := run(New(f, dr, func() time.Time { return t0 })); err != nil {
		t.Fatal(err)
	}
	if len(f.Writes()) != 0 || closedState(t, f, n).State != github.StateOpen {
		t.Errorf("dry run wrote: %v", f.Writes())
	}
}
