package scanner

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

func (h *harness) snapshot() Snapshot {
	h.t.Helper()
	snap, err := loadSnapshot(h.ctx, h.store)
	if err != nil {
		h.t.Fatal(err)
	}
	return snap
}

func (h *harness) rebuilt() Snapshot {
	h.t.Helper()
	snap, err := Rebuild(h.ctx, h.gh, h.cfg, h.role.Name, h.clock)
	if err != nil {
		h.t.Fatal(err)
	}
	return snap
}

func (h *harness) wantRebuildMatches(when string) {
	h.t.Helper()
	live, fresh := h.snapshot(), h.rebuilt()
	if rep := DiffSnapshots(live, fresh); !rep.Empty() {
		h.t.Fatalf("%s: rebuilding from GitHub differs from the database:\n  added   %v\n  removed %v\n  changed %v",
			when, rep.Added, rep.Removed, rep.Changed)
	}
}

func TestRebuildReproducesAScriptedHistory(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.GroupSize = 2 })
	step := func(name string, fs ...Finding) {
		h.t.Helper()
		h.eng.set(fs...)
		h.mustRun()
		h.wantRebuildMatches(name)
	}
	step("file", gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"), gfnd("c", SevMedium, "g"), fnd("d", SevCritical))
	step("one gone", gfnd("a", SevHigh, "g"), gfnd("c", SevMedium, "g"), fnd("d", SevCritical))
	dRow, _ := h.row("d")
	h.close(dRow.Issue, github.ReasonNotPlanned)
	step("d suppressed", gfnd("a", SevHigh, "g"), gfnd("c", SevMedium, "g"), fnd("d", SevCritical))
	aRow, _ := h.row("a")
	h.close(aRow.Issue, github.ReasonCompleted)
	step("regression", gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"), gfnd("c", SevMedium, "g"))
	step("all gone")
	bRow, _ := h.row("b")
	h.close(bRow.Issue, github.ReasonDuplicate)
	step("refile after duplicate close", gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"))
	st := h.snapshot()
	if len(st.Suppressions) == 0 || len(st.Rows) == 0 {
		t.Errorf("the scripted history is too thin: %+v", st)
	}
}

func TestRebuildTreatsOpenAsStrongerThanClosed(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevHigh))
	h.mustRun()
	h.close(1, github.ReasonCompleted)
	h.mustRun() // regression: a is on #2, which is open
	fresh := h.rebuilt()
	if r := fresh.Rows["a"]; r.Issue != 2 || r.Status != StatusOpen {
		t.Errorf("row = %+v", r)
	}
	// Among closed issues the highest number wins.
	h.close(2, github.ReasonCompleted)
	h.eng.set()
	h.mustRun()
	if r := h.rebuilt().Rows["a"]; r.Issue != 2 || r.Status != StatusFixed {
		t.Errorf("row = %+v", r)
	}
}

func TestRebuildKeepsExpiredSuppressions(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevHigh))
	h.mustRun()
	h.close(1, github.ReasonNotPlanned)
	h.mustRun()
	h.clock = h.clock.Add(200 * 24 * time.Hour) // long expired
	sup := h.rebuilt().Suppressions["a"]
	if sup.Issue != 1 || !sup.Until.Before(h.clock) {
		t.Errorf("suppression = %+v", sup)
	}
	h.wantRebuildMatches("expired")
}

func TestRebuildIgnoresPullRequestsAndForeignIssues(t *testing.T) {
	h := newHarness(t, nil)
	h.gh.SeedPR(github.PullRequest{Title: "pr", Labels: []string{labels.Scanner("cve")}, Body: RenderBody("", Parsed{Scanner: "cve", Lines: []Line{{Title: "t", Fingerprint: "pr", Severity: SevLow}}}, nil)})
	h.gh.SeedIssue(github.Issue{Title: "other", Labels: []string{labels.Scanner("lint")}, Body: RenderBody("", Parsed{Scanner: "lint", Lines: []Line{{Title: "t", Fingerprint: "x", Severity: SevLow}}}, nil)})
	if got := h.rebuilt(); len(got.Rows) != 0 {
		t.Errorf("rows = %+v", got.Rows)
	}
}

func TestDiffSnapshotsReport(t *testing.T) {
	old := NewSnapshot([]Row{row("a", 1, StatusOpen, SevLow), row("b", 2, StatusOpen, SevLow)}, []Suppression{{Fingerprint: "s", Until: t0, Issue: 3}})
	fresh := NewSnapshot([]Row{row("a", 1, StatusGone, SevLow), row("c", 4, StatusFixed, SevHigh)}, nil)
	rep := DiffSnapshots(old, fresh)
	if len(rep.Added) != 1 || len(rep.Removed) != 2 || len(rep.Changed) != 1 || rep.Empty() {
		t.Errorf("report = %+v", rep)
	}
	if !DiffSnapshots(old, old.Clone()).Empty() {
		t.Error("a snapshot differs from its clone")
	}
}

// TestRebuildProperty drives the pipeline with random scans and random human
// actions, and after every run requires that rebuilding from GitHub into an
// empty store yields exactly what the live database holds. Seeds are fixed.
//
// Humans may close (completed, not planned, duplicate) and tick boxes. They do
// not reopen: a reopened issue that shares a fingerprint with a newer issue
// can leave the database different from a rebuild until the next rebuild-state
// (the older issue's close is no longer tracked). That is self-limiting, since
// GitHub stays the truth, and TestRunReopenedIssueIsTrackedAgain covers the
// plain reopen.
func TestRebuildProperty(t *testing.T) {
	const seeds, steps = 200, 8
	fps := []string{"f1", "f2", "f3", "f4", "f5", "f6", "f7"}
	for seed := int64(1); seed <= seeds; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			groupSizes := []int{1, 2, 3, 10}
			h := newHarness(t, func(r *config.Role) {
				r.GroupSize = groupSizes[rng.Intn(len(groupSizes))]
				r.MaxOpen = 3 + rng.Intn(10)
				r.Autoclose = rng.Intn(4) != 0
			})
			// Each fingerprint keeps one severity and one group for the whole
			// run (a changed severity is covered by a dedicated test).
			sev := map[string]Severity{}
			grp := map[string]string{}
			for _, fp := range fps {
				sev[fp] = []Severity{SevLow, SevMedium, SevHigh, SevCritical}[rng.Intn(4)]
				grp[fp] = []string{"", "ga", "gb"}[rng.Intn(3)]
			}
			var log []string
			for step := 0; step < steps; step++ {
				// Random human actions on random scanner issues.
				for _, is := range h.all() {
					if rng.Intn(4) != 0 {
						continue
					}
					switch a := rng.Intn(6); {
					case a == 0 && is.State == github.StateOpen:
						h.close(is.Number, github.ReasonCompleted)
						log = append(log, fmt.Sprintf("close #%d completed", is.Number))
					case a == 1 && is.State == github.StateOpen:
						h.close(is.Number, github.ReasonNotPlanned)
						log = append(log, fmt.Sprintf("close #%d not planned", is.Number))
					case a == 2 && is.State == github.StateOpen:
						h.close(is.Number, github.ReasonDuplicate)
						log = append(log, fmt.Sprintf("close #%d duplicate", is.Number))
					case a == 4 && is.State == github.StateOpen:
						body := strings.Replace(is.Body, "- [ ] ", "- [x] ", 1)
						if err := h.fx.UpdateIssueBody(h.ctx, is.Number, body); err != nil {
							t.Fatal(err)
						}
						log = append(log, fmt.Sprintf("tick a box on #%d", is.Number))
					}
				}
				var found []Finding
				for _, fp := range fps {
					if rng.Intn(2) == 0 {
						found = append(found, gfnd(fp, sev[fp], grp[fp]))
					}
				}
				h.eng.set(found...)
				var names []string
				for _, f := range found {
					names = append(names, f.Fingerprint)
				}
				log = append(log, fmt.Sprintf("scan %v", names))
				if _, err := h.run(); err != nil {
					t.Fatalf("step %d: run: %v\nhistory: %v", step, err, log)
				}
				live, fresh := h.snapshot(), h.rebuilt()
				if rep := DiffSnapshots(live, fresh); !rep.Empty() {
					t.Fatalf("step %d: rebuild differs from the database\n  added   %v\n  removed %v\n  changed %v\nhistory: %v",
						step, rep.Added, rep.Removed, rep.Changed, log)
				}
			}
		})
	}
}

func TestRebuildAfterSeverityRaise(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevLow))
	h.mustRun()
	h.eng.set(fnd("a", SevHigh)) // re-rated in place on the open issue
	h.mustRun()
	h.wantRebuildMatches("re-rated")
	h.close(1, github.ReasonNotPlanned)
	h.eng.set(fnd("a", SevCritical)) // raised past the suppression: refiled
	h.mustRun()
	h.wantRebuildMatches("refiled")
}
