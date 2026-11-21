package controller

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/labels"
)

func TestPauseGateCheck(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		cfg    config.Resolved
		issue  *github.Issue // seeded; nil = none
		number int           // 0 = the seeded issue
		fail   error
		want   Reason
	}{
		{name: "config paused", cfg: config.Resolved{Paused: true}, want: ReasonConfig},
		{name: "label present", issue: &github.Issue{Labels: []string{labels.Pause}}, want: ReasonLabel},
		{name: "label absent", issue: &github.Issue{Labels: []string{"other"}}, want: ReasonNone},
		{name: "closed with label", issue: &github.Issue{State: github.StateClosed, Labels: []string{labels.Pause}}, want: ReasonLabel},
		{name: "read error", issue: &github.Issue{}, fail: errors.New("boom"), want: ReasonUnreadable},
		{name: "not found", issue: &github.Issue{}, number: 999, want: ReasonUnreadable},
		{name: "number zero is config only", issue: &github.Issue{Labels: []string{labels.Pause}}, number: -1, want: ReasonNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := fake.New()
			n := 0
			if tc.issue != nil {
				n = f.SeedIssue(*tc.issue)
			}
			if tc.number != 0 {
				n = tc.number
			}
			if tc.number == -1 {
				n = 0
			}
			if tc.fail != nil {
				f.FailNext("GetIssue", tc.fail)
			}
			log, _ := newLog()
			g := NewPauseGate(f, log)
			g.SetStatusIssue(n)
			st := g.Check(ctx, &tc.cfg)
			if st.Reason != tc.want || st.Paused != (tc.want != ReasonNone) {
				t.Errorf("got %+v, want reason %q", st, tc.want)
			}
			if tc.want == ReasonUnreadable && st.Err == nil {
				t.Error("unreadable without Err")
			}
		})
	}
}

func TestPauseGateResumesWhenLabelRemoved(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	n := f.SeedIssue(github.Issue{Labels: []string{labels.Pause}})
	g := NewPauseGate(f, nil)
	g.SetStatusIssue(n)
	if !g.Check(ctx, &config.Resolved{}).Paused {
		t.Fatal("want paused")
	}
	if err := f.RemoveLabel(ctx, n, labels.Pause); err != nil {
		t.Fatal(err)
	}
	if g.Check(ctx, &config.Resolved{}).Paused {
		t.Fatal("want resumed")
	}
}

func TestPauseStopsClaimingLoopsWithinOnePoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		f := fake.New()
		n := f.SeedIssue(github.Issue{})
		log, buf := newLog()
		g := NewPauseGate(f, log)
		g.SetStatusIssue(n)
		claim := &testLoop{name: "claim", claims: true}
		reap := &testLoop{name: "reaper"}
		stop := start(t, mustNew(t, Deps{Log: log, Pause: g, Rand: fixedRand(0.5)}, claim, reap))
		time.Sleep(iv * 3)

		if err := f.AddLabels(ctx, n, labels.Pause); err != nil {
			t.Fatal(err)
		}
		time.Sleep(iv * 11 / 10) // one interval plus jitter: any tick in flight has ended
		c0, r0 := claim.ticks(), reap.ticks()
		time.Sleep(iv * 6)
		if claim.ticks() != c0 {
			t.Errorf("claiming loop ticked %d times while paused", claim.ticks()-c0)
		}
		if reap.ticks() < r0+5 {
			t.Errorf("non-claiming loop stalled: %d -> %d", r0, reap.ticks())
		}

		if err := f.RemoveLabel(ctx, n, labels.Pause); err != nil {
			t.Fatal(err)
		}
		time.Sleep(iv * 3)
		_ = stop()
		if claim.ticks() <= c0 {
			t.Error("claiming loop did not resume")
		}
		if buf.count("loop.paused") != 1 || buf.count("loop.resumed") != 1 {
			t.Errorf("transition logs not once each:\n%s", buf.String())
		}
	})
}
