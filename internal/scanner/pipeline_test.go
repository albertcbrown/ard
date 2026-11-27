package scanner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/costblock"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

func TestRunFilesNewFindings(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevHigh))
	res := h.mustRun()
	if res.Outcome != OutcomeOK || res.Created != 1 || res.Filed != 1 || res.New != 1 || res.Found != 1 || res.SHA != "sha-1" {
		t.Fatalf("res = %+v", res)
	}
	is := h.issue(1)
	if is.Title != "title a" || is.State != github.StateOpen {
		t.Errorf("issue = %+v", is)
	}
	if want := []string{labels.Scanner("cve"), labels.Pending}; strings.Join(is.Labels, ",") != strings.Join(want, ",") {
		t.Errorf("labels = %v, want %v", is.Labels, want)
	}
	contains(t, is.Body, "<!-- ard:fp=a sev=high -->")
	contains(t, is.Body, "<!-- ard:scanner=cve group= -->")
	h.wantRow("a", 1, StatusOpen)
	if got := h.com.paths(); len(got) != 1 || got[0] != ".ard/1/issue.md" {
		t.Errorf("committed %v", got)
	}
	req := h.com.reqs[0]
	if req.Issue != 1 || req.Stage != "scanner" || req.Role != "cve" || req.Summary != "title a" {
		t.Errorf("request = %+v", req)
	}
	contains(t, string(req.Files[0].Data), "sha-1")
	if r := h.lastRun(); r.Outcome != OutcomeOK || r.Found != 1 || r.Filed != 1 || r.SHA != "sha-1" {
		t.Errorf("run row = %+v", r)
	}
}

func TestRunRepeatFilesNothingNew(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevHigh))
	h.mustRun()
	h.eng.sha = "sha-2"
	res := h.mustRun()
	if res.Created != 0 || res.Filed != 0 || res.New != 0 {
		t.Fatalf("res = %+v", res)
	}
	if n := len(h.all()); n != 1 || h.writes("CreateIssue") != 1 {
		t.Errorf("%d issues, %d creates", n, h.writes("CreateIssue"))
	}
	r, _ := h.row("a")
	if r.LastSHA != "sha-2" || !r.LastSeen.After(r.FirstSeen) {
		t.Errorf("last_seen not updated: %+v", r)
	}
}

func TestRunRegressionLinksTheOldIssue(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevHigh))
	h.mustRun()
	h.close(1, github.ReasonCompleted)
	res := h.mustRun()
	if res.Regress != 1 || res.Created != 1 {
		t.Fatalf("res = %+v", res)
	}
	is := h.issue(2)
	contains(t, is.Body, "(regression of #1)")
	contains(t, is.Body, "<!-- ard:fp=a sev=high reg=1 -->")
	h.wantRow("a", 2, StatusOpen)
	// The tombstone was kept until the regression replaced it.
	h.close(2, github.ReasonCompleted)
	h.eng.set()
	h.mustRun()
	h.wantRow("a", 2, StatusFixed)
}

func TestRunGoneChecksTheLineAndAllGoneCloses(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.GroupSize = 5 })
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"))
	h.eng.sha = "sha-1"
	h.mustRun()
	if n := len(h.all()); n != 1 {
		t.Fatalf("want one group issue, got %d", n)
	}
	// b disappears: its line is ticked, the issue stays open.
	h.eng.set(gfnd("a", SevHigh, "g"))
	h.eng.sha = "sha-2"
	res := h.mustRun()
	if res.Edited != 1 || res.Closed != 0 {
		t.Fatalf("res = %+v", res)
	}
	got := ParseBody(h.issue(1).Body)
	if got.Lines[0].Checked || !got.Lines[1].Checked {
		t.Errorf("lines = %+v", got.Lines)
	}
	h.wantRow("a", 1, StatusOpen)
	h.wantRow("b", 1, StatusGone)
	// b comes back: unticked again.
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"))
	h.mustRun()
	if got := ParseBody(h.issue(1).Body); got.Lines[1].Checked {
		t.Errorf("resurfaced line still ticked: %+v", got.Lines)
	}
	h.wantRow("b", 1, StatusOpen)
	// Everything disappears: the issue is closed with the sha comment.
	h.eng.set()
	h.eng.sha = "sha-3"
	res = h.mustRun()
	if res.Closed != 1 {
		t.Fatalf("res = %+v", res)
	}
	is := h.issue(1)
	if is.State != github.StateClosed || is.StateReason != github.ReasonCompleted {
		t.Errorf("issue = %+v", is)
	}
	if cs := h.comments(1); len(cs) != 1 || !strings.Contains(cs[0], "No longer detected at `sha-3`") {
		t.Errorf("comments = %q", cs)
	}
	h.wantRow("a", 1, StatusFixed)
	h.wantRow("b", 1, StatusFixed)
	if h.lastRun().Closed != 1 {
		t.Errorf("run row = %+v", h.lastRun())
	}
}

func TestRunSuppressedByHumanClose(t *testing.T) {
	for _, reason := range []github.StateReason{github.ReasonNotPlanned, github.ReasonDuplicate} {
		t.Run(string(reason), func(t *testing.T) {
			h := newHarness(t, nil)
			h.eng.set(fnd("a", SevHigh))
			h.mustRun()
			h.close(1, reason)
			res := h.mustRun()
			if res.Suppressed != 1 || res.Created != 0 || len(h.all()) != 1 {
				t.Fatalf("res = %+v, %d issues", res, len(h.all()))
			}
			sup, err := h.db.Suppression(h.ctx, "a")
			if err != nil || sup.Issue != 1 || sup.Reason != string(reason) || sup.Severity != "high" {
				t.Fatalf("suppression = %+v, %v", sup, err)
			}
			closedAt := h.issue(1).ClosedAt
			if want := closedAt.Add(90 * 24 * time.Hour); !sup.Until.Equal(want) {
				t.Errorf("until = %s, want closed_at + 90d = %s", sup.Until, want)
			}
			if _, ok := h.row("a"); ok {
				t.Error("a suppressed finding kept a row")
			}
			// Still suppressed on later runs, until it expires.
			h.mustRun()
			if len(h.all()) != 1 {
				t.Error("refiled while suppressed")
			}
			h.clock = h.clock.Add(91 * 24 * time.Hour)
			h.mustRun()
			if len(h.all()) != 2 {
				t.Errorf("not refiled after the suppression expired: %d issues", len(h.all()))
			}
		})
	}
}

func TestRunSeverityRaiseLiftsSuppression(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevMedium))
	h.mustRun()
	h.close(1, github.ReasonNotPlanned)
	h.eng.set(fnd("a", SevMedium))
	h.mustRun()
	if len(h.all()) != 1 {
		t.Fatal("refiled at equal severity")
	}
	h.eng.set(fnd("a", SevCritical))
	res := h.mustRun()
	if res.New != 1 || res.Created != 1 || len(h.all()) != 2 {
		t.Fatalf("res = %+v, %d issues", res, len(h.all()))
	}
	h.wantRow("a", 2, StatusOpen)
	contains(t, h.issue(2).Body, "sev=critical")
}

func TestRunReopenedIssueIsTrackedAgain(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevHigh))
	h.mustRun()
	h.close(1, github.ReasonNotPlanned)
	h.mustRun()
	h.reopen(1)
	h.mustRun()
	h.wantRow("a", 1, StatusOpen)
	if _, err := h.db.Suppression(h.ctx, "a"); !errors.Is(err, scannerdb.ErrNotFound) {
		t.Errorf("suppression survived the reopen: %v", err)
	}
	if len(h.all()) != 1 {
		t.Errorf("%d issues", len(h.all()))
	}
}

func TestRunDeletedIssueDropsTheRow(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevHigh))
	h.mustRun()
	h.close(1, github.ReasonCompleted)
	h.gh.FailNext("GetIssue", github.ErrNotFound) // deleted: 404
	res := h.mustRun()
	if res.Regress != 0 || res.New != 1 || res.Created != 1 {
		t.Fatalf("res = %+v (a deleted issue is forgotten, not a regression)", res)
	}
	h.wantRow("a", 2, StatusOpen)
	contains(t, h.issue(2).Body, "<!-- ard:fp=a sev=high -->")
}

func TestRunHoldIssueIsNeverTouched(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.GroupSize = 5 })
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"))
	h.mustRun()
	h.label(1, labels.Hold)
	before := h.issue(1).Body
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("c", SevLow, "g"))
	res := h.mustRun()
	if res.Deferred != 1 {
		t.Errorf("res = %+v", res)
	}
	if h.issue(1).Body != before || h.writes("UpdateIssue") != 0 || len(h.comments(1)) != 0 {
		t.Errorf("a hold issue was edited: %d updates", h.writes("UpdateIssue"))
	}
	// c does not join the held issue: it files its own.
	if is := h.issue(2); is.Number != 2 || !strings.Contains(is.Body, "ard:fp=c ") {
		t.Errorf("c was not filed separately: %+v", is)
	}
	// Even when everything is gone the held issue stays open.
	h.eng.set()
	h.mustRun()
	if h.issue(1).State != github.StateOpen || len(h.comments(1)) != 0 {
		t.Error("held issue was closed or commented on")
	}
}

func TestRunRunningJobDefersTheClose(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevHigh))
	h.mustRun()
	h.label(1, labels.Running(labels.StageTriage, "security"))
	h.eng.set()
	res := h.mustRun()
	if res.Closed != 0 || res.Deferred != 1 || h.issue(1).State != github.StateOpen {
		t.Fatalf("res = %+v", res)
	}
	if got := ParseBody(h.issue(1).Body); !got.Lines[0].Checked {
		t.Error("the line was not ticked while the close waits")
	}
	if err := h.gh.As("x").RemoveLabel(h.ctx, 1, labels.Running(labels.StageTriage, "security")); err != nil {
		t.Fatal(err)
	}
	res = h.mustRun()
	if res.Closed != 1 || h.issue(1).State != github.StateClosed {
		t.Errorf("deferred close never happened: %+v", res)
	}
}

func TestRunNoAutocloseNeverEditsForAbsence(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.Autoclose = false })
	h.eng.set(fnd("a", SevHigh))
	h.mustRun()
	h.eng.set()
	res := h.mustRun()
	if res.Edited != 0 || res.Closed != 0 || h.writes("UpdateIssue") != 0 || h.issue(1).State != github.StateOpen {
		t.Errorf("res = %+v", res)
	}
	h.wantRow("a", 1, StatusOpen)
	// A raised severity is not absence: the line is re-rated.
	h.eng.set(fnd("a", SevCritical))
	h.mustRun()
	contains(t, h.issue(1).Body, "sev=critical")
}

func TestRunBackpressureDropsBeforeScanning(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.MaxOpen = 1 })
	h.eng.set(fnd("a", SevHigh), fnd("b", SevHigh))
	res := h.mustRun()
	if res.Created != 1 || res.OverBudget != 1 {
		t.Fatalf("res = %+v (max_open 1 files one issue and leaves one for later)", res)
	}
	calls := h.eng.calls
	res = h.mustRun()
	if res.Outcome != OutcomeBackpressure || h.eng.calls != calls {
		t.Fatalf("res = %+v, scans %d -> %d: a dropped run must not start the scanner", res, calls, h.eng.calls)
	}
	if h.lastRun().Outcome != OutcomeBackpressure {
		t.Errorf("run row = %+v", h.lastRun())
	}
	// Closing the open issue frees the slot, and the leftover is filed.
	h.close(1, github.ReasonCompleted)
	h.eng.set(fnd("b", SevHigh))
	res = h.mustRun()
	if res.Created != 1 || res.Outcome != OutcomeOK {
		t.Errorf("res = %+v", res)
	}
}

func TestRunGlobalCapDrops(t *testing.T) {
	h := newHarness(t, nil)
	other := scannerRole("lint")
	h.cfg.Roles = append(h.cfg.Roles, other)
	h.cfg.GlobalMaxOpen = 2
	for i := 0; i < 2; i++ {
		h.gh.SeedIssue(github.Issue{Title: "lint", Labels: []string{labels.Scanner("lint")}})
	}
	h.eng.set(fnd("a", SevHigh))
	res := h.mustRun()
	if res.Outcome != OutcomeGlobalCap || h.eng.calls != 0 {
		t.Fatalf("res = %+v, scans %d", res, h.eng.calls)
	}
	if h.lastRun().Outcome != OutcomeGlobalCap {
		t.Errorf("run row = %+v", h.lastRun())
	}
	// Under the cap, the budget is what is left of it.
	h.cfg.GlobalMaxOpen = 3
	h.eng.set(fnd("a", SevHigh), fnd("b", SevHigh))
	res = h.mustRun()
	if res.Created != 1 || res.OverBudget != 1 {
		t.Errorf("res = %+v", res)
	}
}

func TestRunNoSlotIsNotRecorded(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.failNext(ErrNoSlot)
	h.eng.set(fnd("a", SevHigh))
	res, err := h.run()
	if err != nil || res.Outcome != OutcomeNoSlot {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if _, err := h.db.LastRun(h.ctx); !errors.Is(err, scannerdb.ErrNotFound) {
		t.Errorf("a denied run was recorded (it must stay due): %v", err)
	}
	if len(h.all()) != 0 {
		t.Error("filed without a scan")
	}
	// The next tick offers it again.
	if res := h.mustRun(); res.Created != 1 {
		t.Errorf("res = %+v", res)
	}
}

func TestRunScanFailureChangesNothing(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevHigh))
	h.mustRun()
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"exit":    {&ScanError{SHA: "s", Exit: 1}, OutcomeScanFailed},
		"timeout": {&ScanError{SHA: "s", TimedOut: true}, OutcomeTimeout},
		"decode":  {&ScanError{SHA: "s", Decode: DecodeErrors{{Line: 1, Msg: "bad"}}}, OutcomeScanFailed},
	} {
		h.eng.failNext(tc.err)
		res, err := h.run()
		if err != nil || res.Outcome != tc.want {
			t.Fatalf("%s: res = %+v, err = %v", name, res, err)
		}
		if h.lastRun().Outcome != tc.want {
			t.Errorf("%s: run row = %+v", name, h.lastRun())
		}
	}
	h.eng.set() // a following empty scan is what would close things
	if h.issue(1).State != github.StateOpen || h.writes("UpdateIssue") != 0 {
		t.Error("a failed scan changed GitHub")
	}
	h.wantRow("a", 1, StatusOpen)
}

func TestRunEngineErrorIsAnError(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.failNext(errors.New("git fetch failed"))
	res, err := h.run()
	if err == nil || res.Outcome != OutcomeError || h.lastRun().Outcome != OutcomeError {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

func TestRunHealsACrashBetweenCreateAndRecord(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevHigh))
	h.store.failApply = 1 // the save right after CreateIssue fails
	res, err := h.run()
	if err == nil || res.Outcome != OutcomeError {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if len(h.all()) != 1 {
		t.Fatalf("%d issues after the failed save", len(h.all()))
	}
	if _, ok := h.row("a"); ok {
		t.Fatal("the row was saved despite the failure")
	}
	res = h.mustRun()
	if res.Created != 0 || len(h.all()) != 1 || h.writes("CreateIssue") != 1 {
		t.Fatalf("a duplicate was filed: res = %+v, %d creates", res, h.writes("CreateIssue"))
	}
	h.wantRow("a", 1, StatusOpen)
}

func TestRunJoinsAnUnclaimedGroupIssue(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.GroupSize = 3 })
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"))
	h.mustRun()
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"), gfnd("c", SevLow, "g"), gfnd("d", SevLow, "g"))
	res := h.mustRun()
	if res.Joined != 1 || res.Created != 1 {
		t.Fatalf("res = %+v (c joins #1, d overflows)", res)
	}
	if got := ParseBody(h.issue(1).Body); len(got.Lines) != 3 {
		t.Errorf("lines = %+v", got.Lines)
	}
	h.wantRow("c", 1, StatusOpen)
	h.wantRow("d", 2, StatusOpen)
	if got := h.com.paths(); len(got) != 3 || got[1] != ".ard/1/issue.md" || got[2] != ".ard/2/issue.md" {
		t.Errorf("committed %v", got)
	}
	// The join's issue.md lists all three lines.
	md := string(h.com.reqs[1].Files[0].Data)
	for _, fp := range []string{"a", "b", "c"} {
		contains(t, md, "ard:fp="+fp+" ")
	}
	contains(t, h.issue(1).Title, "findings in g")
}

func TestRunDoesNotJoinAClaimedIssue(t *testing.T) {
	for name, label := range map[string]string{"needs-human": labels.NeedsHuman, "running": labels.Running(labels.StageTriage, "lead")} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, nil)
			h.eng.set(gfnd("a", SevHigh, "g"))
			h.mustRun()
			h.label(1, label)
			h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"))
			res := h.mustRun()
			if res.Joined != 0 || res.Created != 1 {
				t.Errorf("res = %+v", res)
			}
			h.wantRow("b", 2, StatusOpen)
		})
	}
	// A label outside the ard: namespace does not block a join.
	h := newHarness(t, nil)
	h.eng.set(gfnd("a", SevHigh, "g"))
	h.mustRun()
	h.label(1, "bug")
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"))
	if res := h.mustRun(); res.Joined != 1 {
		t.Errorf("res = %+v", res)
	}
}

// hookReader runs a hook right before the first GetIssue.
type hookReader struct {
	Reader
	once func()
}

func (r *hookReader) GetIssue(ctx context.Context, n int) (github.Issue, error) {
	if r.once != nil {
		f := r.once
		r.once = nil
		f()
	}
	return r.Reader.GetIssue(ctx, n)
}

func TestRunJoinFallsBackWhenTheIssueIsClaimedMeanwhile(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(gfnd("a", SevHigh, "g"))
	h.mustRun()
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"))
	// The open list shows #1 as joinable; by the time the join re-fetches it,
	// a human has marked it.
	h.p.Reader = &hookReader{Reader: h.gh, once: func() { h.label(1, labels.NeedsApproval) }}
	res := h.mustRun()
	if res.Joined != 0 || res.Created != 1 {
		t.Errorf("res = %+v", res)
	}
	h.wantRow("b", 2, StatusOpen)
}

func TestRunBudgetFilesMostSevereFirst(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.MaxOpen = 2 })
	h.eng.set(fnd("low", SevLow), fnd("crit", SevCritical), fnd("high", SevHigh))
	res := h.mustRun()
	if res.Created != 2 || res.OverBudget != 1 {
		t.Fatalf("res = %+v", res)
	}
	h.wantRow("crit", 1, StatusOpen)
	h.wantRow("high", 2, StatusOpen)
	if _, ok := h.row("low"); ok {
		t.Error("the least severe was filed past the budget")
	}
}

func TestRunCommitterFailureDoesNotUndoTheIssue(t *testing.T) {
	h := newHarness(t, nil)
	h.com.err = errors.New("push rejected")
	h.eng.set(fnd("a", SevHigh))
	res, err := h.run()
	if err != nil || res.Outcome != OutcomeOK || res.Created != 1 {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	h.wantRow("a", 1, StatusOpen)
}

func TestRunAdoptsHumanChanges(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.GroupSize = 5 })
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevHigh, "g"))
	h.mustRun()
	// A human ticks a's box by hand.
	is := h.issue(1)
	body := strings.Replace(is.Body, "- [ ] title a", "- [x] title a", 1)
	st := h.fx.UpdateIssueBody(h.ctx, 1, body)
	if st != nil {
		t.Fatal(st)
	}
	h.eng.set(gfnd("b", SevHigh, "g"))
	h.mustRun()
	h.wantRow("a", 1, StatusGone)
	// The scanner reports a again: the autoclose scanner unticks it.
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevHigh, "g"))
	h.mustRun()
	if got := ParseBody(h.issue(1).Body); got.Lines[0].Checked {
		t.Errorf("a still ticked: %+v", got.Lines)
	}
	h.wantRow("a", 1, StatusOpen)
}

func TestDryRunNeverReachesGitHubOrTheCommitter(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.MaxOpen = 2 })
	dry, err := effects.NewDryRun(effects.DryRunOptions{Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	h.p.Effects = dry
	h.eng.set(fnd("a", SevHigh), fnd("b", SevMedium), fnd("c", SevLow))
	res := h.mustRun()
	if res.Created != 2 || res.OverBudget != 1 {
		t.Fatalf("res = %+v", res)
	}
	ra, _ := h.row("a")
	rb, _ := h.row("b")
	if ra.Issue >= 0 || rb.Issue >= 0 || ra.Issue == rb.Issue {
		t.Errorf("rows %+v %+v: want distinct synthetic numbers", ra, rb)
	}
	if len(h.com.reqs) != 0 || len(h.gh.Writes()) != 0 {
		t.Errorf("dry-run committed %v / wrote %v", h.com.reqs, h.gh.Writes())
	}
	// The second dry-run proposes nothing new, and max_open is reached from
	// the synthetic issues.
	calls := h.eng.calls
	res = h.mustRun()
	if res.Outcome != OutcomeBackpressure || h.eng.calls != calls {
		t.Errorf("res = %+v", res)
	}
}

func TestDryRunRepeatProposesNothingNew(t *testing.T) {
	h := newHarness(t, nil)
	dry, _ := effects.NewDryRun(effects.DryRunOptions{Log: quiet})
	h.p.Effects = dry
	h.eng.set(fnd("a", SevHigh))
	h.mustRun()
	res := h.mustRun()
	if res.Created != 0 || res.New != 0 {
		t.Errorf("res = %+v", res)
	}
	// It disappears: the synthetic issue is closed in the database.
	h.eng.set()
	res = h.mustRun()
	if res.Closed != 1 {
		t.Fatalf("res = %+v", res)
	}
	r, _ := h.row("a")
	if r.Status != StatusFixed || r.Issue >= 0 {
		t.Errorf("row = %+v", r)
	}
	if len(h.gh.Writes()) != 0 {
		t.Errorf("wrote %v", h.gh.Writes())
	}
}

func TestDryRunJoinsASyntheticGroupIssue(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.GroupSize = 3 })
	dry, _ := effects.NewDryRun(effects.DryRunOptions{Log: quiet})
	h.p.Effects = dry
	h.eng.set(gfnd("a", SevHigh, "g"))
	h.mustRun()
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"))
	res := h.mustRun()
	if res.Joined != 1 || res.Created != 0 {
		t.Fatalf("res = %+v", res)
	}
	ra, _ := h.row("a")
	rb, _ := h.row("b")
	if ra.Issue != rb.Issue || ra.Issue >= 0 {
		t.Errorf("rows %+v %+v", ra, rb)
	}
}

func TestRunStopsWhenReconcileCannotSave(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevHigh))
	h.store.failApply = 1 // the save after CreateIssue
	if _, err := h.run(); err == nil {
		t.Fatal("want an error")
	}
	h.store.failApply = 1 // now the reconcile that adopts the issue
	calls := h.eng.calls
	res, err := h.run()
	if err == nil || res.Outcome != OutcomeError || h.eng.calls != calls {
		t.Fatalf("res = %+v, err = %v, scans %d -> %d: a stale snapshot must not reach the scan", res, err, calls, h.eng.calls)
	}
	if h.lastRun().Outcome != OutcomeError {
		t.Errorf("run row = %+v", h.lastRun())
	}
	h.mustRun()
	if h.writes("CreateIssue") != 1 || len(h.all()) != 1 {
		t.Errorf("%d creates", h.writes("CreateIssue"))
	}
	h.wantRow("a", 1, StatusOpen)
}

func TestDryRunAdoptsLedgerIssuesAfterACrash(t *testing.T) {
	h := newHarness(t, nil)
	dry, err := effects.NewDryRun(effects.DryRunOptions{Log: quiet, LedgerPath: filepath.Join(t.TempDir(), "effects.jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	defer dry.Close()
	h.p.Effects = dry
	h.eng.set(fnd("a", SevHigh), fnd("b", SevLow))
	h.store.failApply = 2 // both saves after the synthetic CreateIssue calls fail
	if _, err := h.run(); err == nil {
		t.Fatal("want an error")
	}
	if _, ok := h.row("a"); ok {
		t.Fatal("rows were saved despite the failure")
	}
	res := h.mustRun()
	if res.Created != 0 || res.New != 0 {
		t.Fatalf("res = %+v: the ledger's issues must be adopted, not re-filed", res)
	}
	h.wantRow("a", -1, StatusOpen)
	issues, err := dry.Issues()
	if err != nil || len(issues) != 2 {
		t.Errorf("ledger issues = %+v, %v", issues, err)
	}
}

func TestRunPausedAfterTheScanFilesNothing(t *testing.T) {
	h := newHarness(t, nil)
	h.p.Paused = func(context.Context) bool { return true }
	h.eng.set(fnd("a", SevHigh))
	res, err := h.run()
	if err != nil || res.Outcome != OutcomePaused {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if len(h.all()) != 0 || len(h.gh.Writes()) != 0 {
		t.Error("a paused run wrote to GitHub")
	}
	if _, err := h.db.LastRun(h.ctx); !errors.Is(err, scannerdb.ErrNotFound) {
		t.Errorf("a paused run was recorded: %v", err)
	}
	h.p.Paused = func(context.Context) bool { return false }
	if res := h.mustRun(); res.Created != 1 {
		t.Errorf("res = %+v", res)
	}
}

func TestDryRunJoinToARealIssueDoesNotCommit(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.GroupSize = 3 })
	h.eng.set(gfnd("a", SevHigh, "g"))
	h.mustRun() // live: a real issue and one commit
	dry, _ := effects.NewDryRun(effects.DryRunOptions{Log: quiet})
	h.p.Effects = dry
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"))
	res := h.mustRun()
	if res.Joined != 1 || len(h.com.reqs) != 1 {
		t.Errorf("res = %+v, %d commits: dry-run must not commit", res, len(h.com.reqs))
	}
}

func TestRunCapsAChecklistThatWouldOverflowTheBody(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.GroupSize = 100000; r.MaxOpen = 20 })
	var fs []Finding
	for i := 0; i < 150; i++ {
		fp := fmt.Sprintf("fp-%03d-%s", i, strings.Repeat("x", 500))
		fs = append(fs, Finding{Fingerprint: fp, Title: strings.Repeat("t", 150), Severity: SevLow, Group: "g", Body: strings.Repeat("d", 1500)})
	}
	h.eng.set(fs...)
	res := h.mustRun()
	if res.Created != 1 || res.OverBudget == 0 || res.Filed+res.OverBudget != 150 {
		t.Fatalf("res = %+v", res)
	}
	if n := len(h.issue(1).Body); n > MaxBodyChars {
		t.Fatalf("body is %d chars, want <= %d", n, MaxBodyChars)
	}
	if got := len(ParseBody(h.issue(1).Body).Lines); got != res.Filed {
		t.Errorf("%d lines for %d filed findings", got, res.Filed)
	}
	// The rest is filed on later runs, never in an oversized body.
	for i := 0; i < 5; i++ {
		h.mustRun()
	}
	for _, is := range h.all() {
		if len(is.Body) > MaxBodyChars {
			t.Errorf("issue #%d body is %d chars", is.Number, len(is.Body))
		}
	}
	for _, f := range fs {
		if _, ok := h.row(f.Fingerprint); !ok {
			t.Fatalf("%s never filed", f.Fingerprint[:10])
		}
	}
}

func TestDryRunAdoptedLedgerIssueCountsTowardsMaxOpen(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.MaxOpen = 2 })
	dry, err := effects.NewDryRun(effects.DryRunOptions{Log: quiet, LedgerPath: filepath.Join(t.TempDir(), "effects.jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	defer dry.Close()
	h.p.Effects = dry
	// One real open issue, and one synthetic issue in the ledger whose rows
	// were never saved (a crash): max_open - 1 real plus one synthetic.
	h.gh.SeedIssue(github.Issue{Title: "real", Labels: []string{labels.Scanner("cve")}})
	body := RenderBody("", Parsed{Scanner: "cve", Lines: []Line{{Title: "t", Fingerprint: "ghost", Severity: SevLow}}}, nil)
	if _, err := dry.CreateIssue(h.ctx, github.NewIssue{Title: "t", Body: body, Labels: []string{labels.Scanner("cve"), labels.Pending}}); err != nil {
		t.Fatal(err)
	}
	h.eng.set(fnd("a", SevHigh))
	res := h.mustRun()
	if res.Outcome != OutcomeBackpressure || h.eng.calls != 0 || res.Created != 0 {
		t.Fatalf("res = %+v, scans %d: the adopted issue must count towards max_open", res, h.eng.calls)
	}
	h.wantRow("ghost", -1, StatusOpen)
}

func TestScannerRewritesKeepTheCostBlock(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.GroupSize = 3 })
	h.p.BodyLocks = &costblock.Locks{}
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"))
	h.mustRun()
	c := 0.5
	blk, _, ok := costblock.Render([]costblock.Row{{Stage: "triage", Role: "lead", Run: 1, CostUSD: &c, Outcome: "done"}}, costblock.MaxBlockBytes)
	if !ok {
		t.Fatal("render")
	}
	nb := costblock.Splice(h.issue(1).Body, blk)
	if err := h.gh.UpdateIssue(h.ctx, 1, github.IssueUpdate{Body: &nb}); err != nil {
		t.Fatal(err)
	}
	// A join (c is added) keeps the block verbatim, at the end.
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"), gfnd("c", SevLow, "g"))
	if res := h.mustRun(); res.Joined != 1 {
		t.Fatalf("res = %+v", res)
	}
	body := h.issue(1).Body
	if len(ParseBody(body).Lines) != 3 || !strings.HasSuffix(body, "\n\n"+blk) || strings.Count(body, costblock.Begin) != 1 {
		t.Fatalf("join lost or moved the block:\n%s", body)
	}
	// An edit (a is gone) keeps it too.
	h.eng.set(gfnd("b", SevLow, "g"), gfnd("c", SevLow, "g"))
	if res := h.mustRun(); res.Edited != 1 {
		t.Fatalf("res = %+v", res)
	}
	body = h.issue(1).Body
	if !strings.HasSuffix(body, "\n\n"+blk) || strings.Count(body, costblock.Begin) != 1 || !ParseBody(body).Lines[0].Checked {
		t.Fatalf("edit lost the block:\n%s", body)
	}
}
