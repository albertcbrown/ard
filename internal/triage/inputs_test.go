package triage

import (
	"errors"
	"time"

	"context"
	"encoding/json"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/state"
)

func TestCleanInputPath(t *testing.T) {
	for _, ok := range []string{"a/b.txt", "data/x-1_2.json", "README"} {
		if err := cleanInputPath(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/abs", "../x", "a/../b", "./a", ".ard/x", ".ard", ".github/w.yml", ".git/config", "a/.git/x",
		"a\\b", "@user/x", "a b", "a`b", "a//b", "a/", strings.Repeat("a", 201)} {
		if err := cleanInputPath(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDecideInputs(t *testing.T) {
	blob := TreeEntry{Mode: "100644", Type: "blob", Size: 10}
	files := map[string]TreeEntry{"ok.txt": blob, "link": {Mode: "120000", Type: "blob"}, "sub": {Mode: "160000", Type: "commit"}, "big": {Mode: "100644", Type: "blob", Size: maxInputBytes + 1}}
	lookup := func(p string) (TreeEntry, bool, error) { e, ok := files[p]; return e, ok, nil }
	h := func(p string) closeout.Input { return closeout.Input{Path: p, Provider: "human"} }
	net := config.Role{Network: true}
	tests := []struct {
		name    string
		in      []closeout.Input
		role    config.Role
		want    fanInVerdict
		reason  string
		missing []string
	}{
		{"present", []closeout.Input{h("ok.txt")}, net, fReady, rReady, nil},
		{"missing", []closeout.Input{h("ok.txt"), h("nope.txt")}, net, fWait, rWaitingInputs, []string{"nope.txt"}},
		{"symlink", []closeout.Input{h("link")}, net, fWait, rWaitingInputs, []string{"link"}},
		{"submodule", []closeout.Input{h("sub")}, net, fWait, rWaitingInputs, []string{"sub"}},
		{"too big", []closeout.Input{h("big")}, net, fHuman, rInputTooBig, nil},
		{"bad path", []closeout.Input{h("../x")}, net, fHuman, rBadInputPath, nil},
		{"worker without network", []closeout.Input{{Path: "w.txt", Provider: "worker"}}, config.Role{}, fHuman, rWorkerNoNet, nil},
		{"worker with network", []closeout.Input{{Path: "w.txt", Provider: "worker"}}, net, fReady, rReady, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decideInputs(tc.in, tc.role, true, lookup)
			if got.Verdict != tc.want || got.Reason != tc.reason || strings.Join(got.Missing, ",") != strings.Join(tc.missing, ",") {
				t.Errorf("got %+v", got)
			}
		})
	}
}

// seedEngineerDone records a closed triage engineer done carrying inputs.
func (h *harness) seedEngineerDone(n int, inputs []closeout.Input) {
	h.t.Helper()
	ctx := context.Background()
	sj, err := h.store.CreateJob(ctx, state.NewJob{Stage: "triage", Role: "engineer", Issue: n})
	if err != nil {
		h.t.Fatal(err)
	}
	b, _ := json.Marshal(closeout.Record{Version: closeout.RecordVersion, Phase: closeout.PhaseClosed,
		Job:  closeout.Job{ID: sj.ID, Stage: "triage", Role: "engineer", Issue: n},
		Plan: closeout.Plan{Outcome: closeout.OutcomeDone, Rearmed: true, Inputs: inputs}})
	if err := h.store.SetCloseout(ctx, sj.ID, string(closeout.PhaseClosed), b); err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.Complete(ctx, sj.ID, nil); err != nil {
		h.t.Fatal(err)
	}
}

func TestGateHoldsForHumanInput(t *testing.T) {
	h := gateHarness(t, false)
	n := h.ready("ard:triage:engineer", "ard:triage:engineer:done")
	h.seedWorkerCloseout(n, true)
	h.seedEngineerDone(n, []closeout.Input{{Path: "data/spec.txt", Provider: "human"}})
	h.mustTick()
	if h.has(n, "ard:worker:engineer") {
		t.Fatal("worker started with a missing input")
	}
	if c := commentsWith(h, n, "data/spec.txt"); c != 1 || commentsWith(h, n, "capture note") != 1 {
		t.Fatalf("waiting comment posted %d times", c)
	}
	h.mustTick()
	if c := commentsWith(h, n, "waiting for inputs"); c != 1 {
		t.Errorf("waiting comment repeated: %d", c)
	}
	if h.shaCalls != 2 {
		t.Errorf("main SHA read %d times over two ticks, want 1 per tick", h.shaCalls)
	}
	// The file lands: a fresh "ready for approval" is posted, no worker yet.
	h.mainFiles = map[string]TreeEntry{"data/spec.txt": {Mode: "100644", Type: "blob", Size: 5}}
	h.mainSHAval = "sha2"
	h.mustTick()
	if h.has(n, "ard:worker:engineer") {
		t.Fatal("a network round started a worker without approval")
	}
	if c := commentsWith(h, n, "ready for approval"); c != 1 {
		t.Errorf("ready for approval posted %d times", c)
	}
}

func TestGateNetworkRoundNeedsApprovalWithoutInputs(t *testing.T) {
	h := gateHarness(t, false)
	n := h.ready("ard:triage:engineer", "ard:triage:engineer:done")
	h.seedWorkerCloseout(n, true)
	h.seedEngineerDone(n, nil)
	h.mustTick()
	if h.has(n, "ard:worker:engineer") || commentsWith(h, n, "ready for approval") != 1 {
		t.Fatalf("network round without approval: labels %v", h.issue(n).Labels)
	}
	if h.shaCalls != 0 {
		t.Errorf("main read with no inputs: %d", h.shaCalls)
	}
}

func TestGateBadInputPathAndNoNetworkWorker(t *testing.T) {
	h := gateHarness(t, false)
	n := h.ready("ard:triage:engineer", "ard:triage:engineer:done")
	h.seedWorkerCloseout(n, true)
	h.seedEngineerDone(n, []closeout.Input{{Path: ".github/workflows/x.yml", Provider: "human"}})
	h.mustTick()
	if !h.has(n, "ard:needs-human") || h.entryCalls != 0 {
		t.Errorf("bad path: labels %v, entry reads %d", h.issue(n).Labels, h.entryCalls)
	}
}

var specInput = []closeout.Input{{Path: "data/spec.txt", Provider: "human"}}

func landSpec(h *harness) {
	h.mainFiles = map[string]TreeEntry{"data/spec.txt": {Mode: "100644", Type: "blob", Size: 5}}
}

// engineerDoneAt puts the engineer's :done label in the timeline.
func (h *harness) engineerDoneAt(n int, at time.Time) {
	h.gh.SeedTimeline(n, github.TimelineEvent{Event: "labeled", Label: "ard:triage:engineer:done", Actor: self, CreatedAt: at})
}

func TestApprovalMarkerPerRound(t *testing.T) {
	after := tDone.Add(time.Minute)
	t.Run("round zero keeps the old marker", func(t *testing.T) {
		h := gateHarness(t, true)
		n := h.ready()
		h.mustTick()
		if commentsWith(h, n, "gate=approval job=") != 1 || commentsWith(h, n, "round=") != 0 {
			t.Errorf("comments = %+v", h.comments(n))
		}
	})
	t.Run("a network round puts the round in the marker", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.ready("ard:triage:engineer:done")
		h.seedWorkerCloseout(n, true)
		h.seedEngineerDone(n, nil)
		h.mustTick()
		if commentsWith(h, n, "round=1 -->") != 1 || commentsWith(h, n, "ready for approval") != 1 {
			t.Errorf("comments = %+v", h.comments(n))
		}
	})
	t.Run("an approval older than the engineer done is ignored", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.ready(labels.Approved, "ard:triage:engineer:done")
		h.labelledBy(n, labels.Approved, "maint", "triage", tDone.Add(-time.Minute))
		h.engineerDoneAt(n, tDone)
		h.seedWorkerCloseout(n, true)
		h.seedEngineerDone(n, nil)
		h.mustTick()
		if h.has(n, "ard:worker:engineer") || commentsWith(h, n, "ready for approval") != 1 {
			t.Errorf("labels %v, comments %+v", h.issue(n).Labels, h.comments(n))
		}
	})
	t.Run("an approval given while inputs are missing counts once they land", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.ready(labels.Approved, "ard:triage:engineer:done")
		h.labelledBy(n, labels.Approved, "maint", "triage", after)
		h.engineerDoneAt(n, tDone)
		h.seedWorkerCloseout(n, true)
		h.seedEngineerDone(n, specInput)
		h.mustTick()
		if h.has(n, "ard:worker:engineer") {
			t.Fatal("started with a missing input")
		}
		landSpec(h)
		h.mainSHAval = "sha2"
		h.mustTick()
		if !h.has(n, "ard:worker:engineer") {
			t.Errorf("not started: labels %v", h.issue(n).Labels)
		}
	})
	t.Run("an unclosed bounced worker record still forces approval", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.ready("ard:triage:engineer:done")
		h.seedWorkerPhase(n, true, closeout.PhasePrepared)
		h.seedEngineerDone(n, nil)
		h.mustTick()
		if h.has(n, "ard:worker:engineer") || commentsWith(h, n, "ready for approval") != 1 {
			t.Errorf("labels %v", h.issue(n).Labels)
		}
	})
	t.Run("a non-network issue still starts without approval", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.ready()
		h.mustTick()
		if !h.has(n, "ard:worker:engineer") {
			t.Errorf("labels %v", h.issue(n).Labels)
		}
	})
}

func TestRerunBeforeGate(t *testing.T) {
	h := gateHarness(t, false)
	// The re-armed lead is pending and its :done is gone: the gate waits.
	n := h.gh.SeedIssue(github.Issue{Title: "t", Body: "b", Author: self, Labels: []string{"ard:pending", "ard:triage:engineer:done"}, CreatedAt: t0, UpdatedAt: t0})
	h.seedWorkerCloseout(n, true)
	h.seedEngineerDone(n, nil)
	h.mustTick()
	if h.has(n, "ard:worker:engineer") || commentsWith(h, n, "ready for approval") != 0 {
		t.Errorf("gate acted before the lead finished: %v", h.issue(n).Labels)
	}
}

func TestGateMainReadErrorWaits(t *testing.T) {
	h := gateHarness(t, false)
	n := h.ready("ard:triage:engineer:done")
	h.seedWorkerCloseout(n, true)
	h.seedEngineerDone(n, specInput)
	h.mainErr = errors.New("fetch failed")
	h.mustTick()
	if h.has(n, labels.NeedsHuman) || h.has(n, "ard:worker:engineer") {
		t.Errorf("labels %v", h.issue(n).Labels)
	}
	if len(h.events(state.EventGate)) != 0 {
		t.Error("a read error recorded a gate event")
	}
	h.mainErr = nil
	landSpec(h)
	h.mustTick()
	if commentsWith(h, n, "ready for approval") != 1 {
		t.Errorf("comments = %+v", h.comments(n))
	}
}

func TestGateInputSymlinkSubmodule(t *testing.T) {
	for _, e := range []TreeEntry{{Mode: "120000", Type: "blob", Size: 5}, {Mode: "160000", Type: "commit"}} {
		h := gateHarness(t, false)
		n := h.ready("ard:triage:engineer:done")
		h.seedWorkerCloseout(n, true)
		h.seedEngineerDone(n, specInput)
		h.mainFiles = map[string]TreeEntry{"data/spec.txt": e}
		h.mustTick()
		if commentsWith(h, n, "waiting for inputs") != 1 || h.has(n, "ard:worker:engineer") || commentsWith(h, n, "ready for approval") != 0 {
			t.Errorf("mode %s: comments %+v", e.Mode, h.comments(n))
		}
	}
}

func TestMainReadOncePerTickAndCacheDropsOnNewSHA(t *testing.T) {
	h := gateHarness(t, false)
	for i := 0; i < 3; i++ {
		n := h.ready("ard:triage:engineer:done")
		h.seedWorkerCloseout(n, true)
		h.seedEngineerDone(n, specInput)
	}
	landSpec(h)
	h.mustTick()
	if h.shaCalls != 1 || h.entryCalls != 1 {
		t.Errorf("one tick: sha reads %d, entry reads %d; want 1, 1", h.shaCalls, h.entryCalls)
	}
	h.mustTick()
	if h.shaCalls != 2 || h.entryCalls != 1 {
		t.Errorf("same SHA: sha reads %d, entry reads %d; want 2, 1", h.shaCalls, h.entryCalls)
	}
	h.mainSHAval = "sha2"
	h.mustTick()
	if h.entryCalls != 2 {
		t.Errorf("new SHA: entry reads %d, want 2", h.entryCalls)
	}
}

func TestStatusEventsWaitingForInputs(t *testing.T) {
	h := gateHarness(t, false)
	n := h.ready("ard:triage:engineer:done")
	h.seedWorkerCloseout(n, true)
	h.seedEngineerDone(n, specInput)
	h.mustTick()
	h.mustTick()
	if es := h.events(state.EventGate); len(es) != 1 || !strings.Contains(string(es[0].Data), "waiting-for-inputs") {
		t.Fatalf("events = %+v", es)
	}
	landSpec(h)
	h.mainSHAval = "sha2"
	h.mustTick()
	es := h.events(state.EventGate)
	if len(es) != 2 || !strings.Contains(string(es[1].Data), "inputs-ready") {
		t.Errorf("events = %+v", es)
	}
}
