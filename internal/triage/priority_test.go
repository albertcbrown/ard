package triage

import (
	"testing"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

func (h *harness) appIssue(lbls ...string) int {
	return h.gh.SeedIssue(github.Issue{Title: "t", Body: "b", Author: self, Labels: append([]string{lead}, lbls...), CreatedAt: t0, UpdatedAt: t0})
}

func TestCandidatePriority(t *testing.T) {
	h := newHarness(t)
	p3 := h.appIssue(labels.P3)
	plain := h.appIssue()
	p1 := h.appIssue(labels.P1, labels.P3)
	h.mustTick()
	got := map[int]int{}
	for _, c := range h.pool.last {
		got[c.Number] = c.Priority
	}
	want := map[int]int{p3: 3, plain: 2, p1: 1}
	for n, w := range want {
		if g, ok := got[n]; !ok || g != w {
			t.Errorf("issue %d priority = %d (offered %v), want %d", n, g, ok, w)
		}
	}
}

func TestP0BeyondOldest50IsFoundOnce(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 55; i++ {
		h.appIssue()
	}
	p0 := h.appIssue(labels.P0)
	h.mustTick()
	seen := map[int]bool{}
	var found bool
	for _, c := range h.pool.last {
		if seen[c.Number] {
			t.Errorf("issue %d offered twice", c.Number)
		}
		seen[c.Number] = true
		if c.Number == p0 {
			found = c.Priority == 0
		}
	}
	if !found {
		t.Fatalf("P0 issue %d not offered with priority 0", p0)
	}
}
