package reviewer

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

func running(l string) string { return labels.Running(labels.StageReview, l) }

func (h *harness) runningJobs() int {
	h.t.Helper()
	js, err := h.store.RunningJobs(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return len(js)
}

func TestTickDryRunLogsAndClaimsNothing(t *testing.T) {
	h := newHarness(t, dryRun)
	pr, _ := h.scenario()
	h.mustTick()
	if h.git.nFetches() != 0 || h.runningJobs() != 0 || h.ex.nPrepared() != 0 || h.has(pr, running(reviewer)) {
		t.Errorf("dry-run claimed: fetches %d jobs %d", h.git.nFetches(), h.runningJobs())
	}
	if got := readLedger(t, h.ledger); strings.Contains(got, "AddLabels") {
		t.Errorf("ledger:\n%s", got)
	}
}

func TestTickWithoutCredentialClaimsNothing(t *testing.T) {
	h := newHarness(t, noCred)
	h.scenario()
	h.mustTick()
	if h.git.nFetches() != 0 || h.runningJobs() != 0 {
		t.Error("claimed without a model credential")
	}
}

func TestPinFailuresClaimNothing(t *testing.T) {
	tests := []struct {
		name string
		set  func(*fakeGit)
	}{
		{"head moved", func(g *fakeGit) { g.tips = []string{"9999999999999999999999999999999999999999"} }},
		{"fetch error", func(g *fakeGit) { g.fetchErr = errBoom }},
		{"merge base error", func(g *fakeGit) { g.mbErr = errBoom }},
		{"diff error", func(g *fakeGit) { g.diffErr = errBoom }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			pr, _ := h.scenario()
			tc.set(h.git)
			h.mustTick()
			if h.runningJobs() != 0 || h.ex.nPrepared() != 0 || h.has(pr, running(reviewer)) || len(h.fin.got()) != 0 {
				t.Error("something was claimed")
			}
			// The request stays, so the next poll retries.
			if !h.has(pr, labels.Request(labels.StageReview, reviewer)) {
				t.Error("request label lost")
			}
		})
	}
}

func TestClaimAndRun(t *testing.T) {
	h := newHarness(t)
	h.git.patch = " a.go | 1 +\n"
	pr, issue := h.scenario(func(s *scenario) { s.pr.Labels = []string{labels.Request(labels.StageReview, reviewer)} })
	h.gh.SeedComment(issue, github.Comment{Author: "bob", Body: "an issue comment", CreatedAt: t0})
	h.mustTick()

	// The claim record and the running label (on the PR).
	js, _ := h.store.RunningJobs(context.Background())
	if len(js) != 1 {
		t.Fatalf("jobs = %d", len(js))
	}
	j := js[0]
	if j.Stage != "review" || j.Role != reviewer || j.Issue != issue || j.PR != pr || j.Branch != fmt.Sprintf("worker/engineer-%d-fix-it", issue) {
		t.Errorf("job = %+v", j)
	}
	if !h.has(pr, running(reviewer)) || h.has(issue, running(reviewer)) {
		t.Errorf("labels PR %v issue %v", h.prLabels(pr), h.prLabels(issue))
	}
	// The executor job.
	if len(h.ex.prepared) != 1 {
		t.Fatalf("prepared = %d", len(h.ex.prepared))
	}
	ej := h.ex.prepared[0]
	if ej.Stage != "review" || ej.Role != reviewer || ej.Issue != issue || ej.Ref != tipSHA || ej.ID != j.ID || ej.ReadOnly || ej.Repo != "o/r" {
		t.Errorf("executor job = %+v", ej)
	}
	if want := []string{fmt.Sprintf(".ard/%d/review-%s.md", issue, reviewer)}; fmt.Sprint(ej.Scrub) != fmt.Sprint(want) {
		t.Errorf("scrub = %v, want %v", ej.Scrub, want)
	}
	dir := fmt.Sprintf(".ard-input/%d/", issue)
	files := map[string]string{}
	for _, f := range ej.Inputs {
		files[f.Path] = string(f.Data)
	}
	if len(files) != 3 || !strings.HasPrefix(files[dir+"diff.patch"], "UNTRUSTED INPUT") || !strings.Contains(files[dir+"diff.patch"], "Head: "+tipSHA) ||
		!strings.Contains(files[dir+"diff.patch"], "a.go | 1 +") || !strings.Contains(files[dir+"issue.md"], "It is broken.") || !strings.Contains(files[dir+"comments.md"], "an issue comment") {
		t.Errorf("inputs = %v", files)
	}
	// The diff was taken from the merge base to the pinned tip with .ard/ left out.
	if len(h.git.diffCalls) != 1 || fmt.Sprint(h.git.diffCalls[0]) != fmt.Sprint([]string{baseSHA, tipSHA, fmt.Sprintf(".ard/%d/", issue)}) {
		t.Errorf("diff calls = %v", h.git.diffCalls)
	}
	// The command names the untrusted paths and carries no credential on argv.
	if len(h.ex.started) != 1 {
		t.Fatalf("started = %d", len(h.ex.started))
	}
	argv := strings.Join(h.ex.started[0].Args, " ")
	for _, want := range []string{dir + "diff.patch", dir + "issue.md", dir + "comments.md", fmt.Sprintf(".ard/%d", issue)} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv lacks %s:\n%s", want, argv)
		}
	}
	if strings.Contains(argv, tokenVal) {
		t.Error("credential on the command line")
	}
	// FinishWith gets the review context.
	calls := h.fin.got()
	if len(calls) != 1 || calls[0].JobID != j.ID {
		t.Fatalf("finish calls = %+v", calls)
	}
	rc := calls[0].Opts.Review
	if rc == nil || rc.MergeBase != baseSHA || rc.HeadMoved || fmt.Sprint(rc.Added) != fmt.Sprint(h.git.added) {
		t.Errorf("review context = %+v", rc)
	}
	if calls[0].Opts.Cause.Kind != "" {
		t.Errorf("cause = %+v", calls[0].Opts.Cause)
	}
	// The loop never marked the PR ready or touched its request label.
	if !h.has(pr, labels.Request(labels.StageReview, reviewer)) {
		t.Error("request label removed by the loop")
	}
	for _, c := range h.gh.Writes() {
		if c.Method == "MarkReady" || c.Method == "UpdatePR" || c.Method == "CreateReview" {
			t.Errorf("the loop called %s", c.Method)
		}
	}
}

func TestHeadMovedDuringReview(t *testing.T) {
	moved := "3333333333333333333333333333333333333333"
	tests := []struct {
		name string
		set  func(*fakeGit)
		want bool
	}{
		{"same head", func(g *fakeGit) {}, false},
		{"new head, only .ard/ changed", func(g *fakeGit) { g.tips = []string{tipSHA, moved} }, false},
		{"new head with code", func(g *fakeGit) { g.tips = []string{tipSHA, moved}; g.changed = []string{"a.go"} }, true},
		{"cannot compare", func(g *fakeGit) { g.tips = []string{tipSHA, moved}; g.changedErr = errBoom }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.scenario()
			// RevParse is called by the pin, then by the check: tips are indexed by call.
			h.git.tips = []string{tipSHA, tipSHA}
			tc.set(h.git)
			h.mustTick()
			calls := h.fin.got()
			if tc.name != "same head" && fmt.Sprint(h.git.changedEx) != "[.ard/1/]" {
				t.Errorf("CodeChanged excludes %v", h.git.changedEx)
			}
			if len(calls) != 1 || calls[0].Opts.Review == nil || calls[0].Opts.Review.HeadMoved != tc.want {
				t.Fatalf("calls = %+v, want HeadMoved %v", calls, tc.want)
			}
		})
	}
}

func TestPrepareErrorsFinishWithACause(t *testing.T) {
	for _, tc := range []struct {
		name  string
		set   func(*harness)
		kind  closeout.CauseKind
		retry bool
	}{
		{"clone", func(h *harness) { h.ex.prepareErr = fmt.Errorf("x: %w", executor.ErrClone) }, closeout.CauseCloneFail, true},
		{"inputs too large", func(h *harness) { h.ex.prepareErr = fmt.Errorf("x: %w", executor.ErrInputsTooLarge) }, closeout.CauseInputsTooLarge, false},
		{"prepare", func(h *harness) { h.ex.prepareErr = errBoom }, closeout.CauseNoProcess, false},
		{"start", func(h *harness) { h.ex.startErr = errBoom }, closeout.CauseNoProcess, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.scenario()
			tc.set(h)
			h.mustTick()
			calls := h.fin.got()
			if len(calls) != 1 || calls[0].Opts.Cause.Kind != tc.kind || calls[0].Opts.Cause.Retry != tc.retry {
				t.Fatalf("calls = %+v", calls)
			}
			if calls[0].Opts.Review == nil || calls[0].Opts.Review.MergeBase != baseSHA {
				t.Errorf("review = %+v", calls[0].Opts.Review)
			}
		})
	}
}

func TestRunningLabelFailureFailsTheClaim(t *testing.T) {
	h := newHarness(t)
	pr, _ := h.scenario()
	h.gh.FailNext("AddLabels", errBoom)
	h.mustTick()
	if h.runningJobs() != 0 || h.ex.nPrepared() != 0 || len(h.fin.got()) != 0 || h.has(pr, running(reviewer)) {
		t.Error("a failed label must leave no running job")
	}
	// The next tick can claim again.
	h.mustTick()
	if len(h.fin.got()) != 1 {
		t.Errorf("second tick: finish calls = %d", len(h.fin.got()))
	}
}

func TestShutdownLeavesTheJobRunning(t *testing.T) {
	h := newHarness(t)
	pr, _ := h.scenario()
	h.ex.running = make(chan struct{})
	in, err := h.tickAsync()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for h.runningJobs() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	close(h.stop)
	select {
	case <-in.Wait():
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not stop")
	}
	if len(h.fin.got()) != 0 || h.runningJobs() != 1 || !h.has(pr, running(reviewer)) {
		t.Errorf("shutdown finished the job: %+v", h.fin.got())
	}
}

func TestPreviousReviewInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		file bool
	}{{"first review", false}, {"later round", true}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			_, issue := h.scenario(func(s *scenario) { s.pr.Labels = []string{labels.Request(labels.StageReview, reviewer)} })
			path := fmt.Sprintf(".ard/%d/review-%s.md", issue, reviewer)
			if tc.file {
				// Read at the pinned tip, which holds the earlier close-out commit.
				h.git.files = map[string]string{tipSHA + ":" + path: "EARLIER FINDING"}
			}
			h.mustTick()
			if len(h.ex.prepared) != 1 {
				t.Fatalf("prepared = %d", len(h.ex.prepared))
			}
			var got string
			for _, f := range h.ex.prepared[0].Inputs {
				if f.Path == fmt.Sprintf(".ard-input/%d/previous-review.md", issue) {
					got = string(f.Data)
				}
			}
			if !tc.file {
				if got != "" {
					t.Errorf("a first review got previous-review.md:\n%s", got)
				}
				return
			}
			if !strings.HasPrefix(got, "UNTRUSTED INPUT") || !strings.Contains(got, "EARLIER FINDING") {
				t.Errorf("previous-review.md = %q", got)
			}
			if argv := strings.Join(h.ex.started[0].Args, " "); !strings.Contains(argv, "previous-review.md") {
				t.Errorf("previous-review.md is not named untrusted:\n%s", argv)
			}
		})
	}
}

func TestPreviousReviewOversize(t *testing.T) {
	h := newHarness(t)
	_, issue := h.scenario(func(s *scenario) { s.pr.Labels = []string{labels.Request(labels.StageReview, reviewer)} })
	path := fmt.Sprintf(".ard/%d/review-%s.md", issue, reviewer)
	// A committed role file may be up to MaxRoleFileBytes; the input is cut to 64 KiB.
	h.git.files = map[string]string{tipSHA + ":" + path: strings.Repeat("x", 200<<10)}
	h.mustTick()
	if len(h.ex.prepared) != 1 {
		t.Fatalf("prepared = %d, want the review to run", len(h.ex.prepared))
	}
	var got string
	for _, f := range h.ex.prepared[0].Inputs {
		if f.Path == fmt.Sprintf(".ard-input/%d/previous-review.md", issue) {
			got = string(f.Data)
		}
	}
	if !strings.HasSuffix(got, "\n[truncated]\n") || len(got) > 70<<10 {
		t.Errorf("previous-review.md: len %d, want truncated to about 64 KiB", len(got))
	}
}
