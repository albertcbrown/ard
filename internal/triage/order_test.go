package triage

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
)

func TestHeldBy(t *testing.T) {
	eng := func(after ...string) config.Role { return config.Role{Name: "engineer", After: after} }
	tests := []struct {
		name     string
		role     config.Role
		have     []string
		wantDep  string
		wantHeld bool
	}{
		{"no after", eng(), []string{"ard:triage:architect"}, "", false},
		{"dependency never requested", eng("architect"), []string{"ard:triage:engineer"}, "", false},
		{"requested, not done", eng("architect"), []string{"ard:triage:architect"}, "architect", true},
		{"requested and done", eng("architect"), []string{"ard:triage:architect", "ard:triage:architect:done"}, "", false},
		{"only running", eng("architect"), []string{"ard:triage:architect:running"}, "architect", true},
		{"lead held while pending", eng("lead"), []string{"ard:pending"}, "lead", true},
		{"lead done, pending removed", eng("lead"), []string{"ard:triage:lead:done"}, "", false},
		{"first holding dependency", eng("pm", "architect"), []string{"ard:triage:architect", "ard:triage:pm", "ard:triage:pm:done"}, "architect", true},
		{"self ignored", eng("engineer"), []string{"ard:triage:engineer"}, "", false},
		{"case-insensitive", eng("architect"), []string{"ARD:Triage:Architect"}, "architect", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dep, held := heldBy(tc.role, tc.have)
			if dep != tc.wantDep || held != tc.wantHeld {
				t.Errorf("heldBy = %q, %v; want %q, %v", dep, held, tc.wantDep, tc.wantHeld)
			}
		})
	}
}

// timelineCounter counts ListTimeline calls.
type timelineCounter struct {
	Reader
	n int
}

func (c *timelineCounter) ListTimeline(ctx context.Context, n int) ([]github.TimelineEvent, error) {
	c.n++
	return c.Reader.ListTimeline(ctx, n)
}

func afterHarness(t *testing.T, opts ...option) *harness {
	h := newHarness(t, opts...)
	h.cfg.Roles = append(h.cfg.Roles, config.Role{Kind: config.KindTriage, Name: "engineer", Dir: ".ard/triage/engineer", Agent: "claude",
		Timeout: 30 * time.Minute, After: []string{"architect"}})
	return h
}

func claimedRoles(h *harness) []string {
	var out []string
	for _, j := range h.ex.prepared {
		out = append(out, j.Role)
	}
	return out
}

func TestAfterOrdering(t *testing.T) {
	arch, eng := "ard:triage:architect", "ard:triage:engineer"
	t.Run("held until the prerequisite is done", func(t *testing.T) {
		h := afterHarness(t)
		n := h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{arch, eng}, CreatedAt: t0})
		h.mustTick()
		if got := claimedRoles(h); len(got) != 1 || got[0] != "architect" {
			t.Fatalf("claimed %v, want [architect]", got)
		}
		// The harness Finisher is a fake: finish the architect by hand.
		for _, j := range h.jobs() {
			if err := h.store.Complete(context.Background(), j.ID, nil); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.gh.RemoveLabel(context.Background(), n, arch+":running"); err != nil {
			t.Fatal(err)
		}
		if err := h.gh.AddLabels(context.Background(), n, arch+":done"); err != nil {
			t.Fatal(err)
		}
		h.mustTick()
		if got := claimedRoles(h); len(got) != 2 || got[1] != "engineer" {
			t.Fatalf("claimed %v, want engineer second", got)
		}
	})
	t.Run("requested alone is claimed", func(t *testing.T) {
		h := afterHarness(t)
		h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{eng}, CreatedAt: t0})
		h.mustTick()
		if got := claimedRoles(h); len(got) != 1 || got[0] != "engineer" {
			t.Fatalf("claimed %v", got)
		}
	})
	t.Run("dry run does not log a held role", func(t *testing.T) {
		var buf strings.Builder
		h := afterHarness(t, dryRun)
		h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{arch, eng}, CreatedAt: t0})
		log := slog.New(slog.NewTextHandler(&buf, nil))
		if err := h.loop.Tick(context.Background(), controller.Env{Log: log, Cfg: h.cfg, Inflight: controller.NewInflight(context.Background(), quiet), Stopping: h.stop}); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if !strings.Contains(out, "triage.would_claim") || !strings.Contains(out, "role=architect") || strings.Contains(out, "role=engineer reason") {
			t.Errorf("log = %s", out)
		}
	})
	t.Run("held human issue costs no timeline read", func(t *testing.T) {
		h := afterHarness(t)
		tc := &timelineCounter{Reader: h.loop.d.Reader}
		h.loop.d.Reader = tc
		h.human("t", "b", arch+":running", eng)
		h.mustTick()
		if tc.n != 0 || len(h.ex.prepared) != 0 {
			t.Errorf("timeline reads %d, prepared %d", tc.n, len(h.ex.prepared))
		}
	})
}
