package closeout

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/state"
)

func (e *env) lead() Job {
	e.write(".ard/7/lead.md", roleFile("lead", "done", "next: [triage:architect, review:license-review]", "worker: engineer"))
	e.write(".ard/7/plan.md", "the plan")
	return e.claim(StageTriage, "lead")
}

// assertLeadClosed checks the end state of a lead close-out.
func (e *env) assertLeadClosed(j Job) {
	e.t.Helper()
	l := e.labels(7)
	for _, gone := range []string{"ard:triage:lead:running", "ard:pending"} {
		if l[gone] {
			e.t.Errorf("%s still present: %v", gone, l)
		}
	}
	for _, want := range []string{"ard:triage:lead:done", "ard:triage:architect", "ard:worker:engineer:running"} {
		if !l[want] {
			e.t.Errorf("%s missing: %v", want, l)
		}
	}
	if n := e.markerComments(7, j.ID); n != 1 {
		e.t.Errorf("marker comments = %d, want 1", n)
	}
	if len(e.cm.commits) != 1 {
		e.t.Errorf("distinct commits = %d, want 1", len(e.cm.commits))
	}
	sj := e.sjob(j.ID)
	if sj.CloseoutPhase != string(PhaseClosed) || sj.Status != state.StatusDone {
		e.t.Errorf("job = phase %q status %s", sj.CloseoutPhase, sj.Status)
	}
	if _, err := os.Stat(filepath.Join(e.staging, j.ID)); !errors.Is(err, os.ErrNotExist) {
		e.t.Errorf("staging dir remains: %v", err)
	}
}

func TestRunLeadDone(t *testing.T) {
	e := newEnv(t)
	j := e.lead()
	out, err := New(e.d).Run(context.Background(), j)
	if err != nil || out != OutcomeDone {
		t.Fatalf("Run = %s, %v", out, err)
	}
	e.assertLeadClosed(j)
	for _, want := range []string{".ard/7/lead.md", ".ard/7/plan.md", logPath} {
		found := false
		for _, files := range e.cm.commits {
			if _, ok := files[want]; ok {
				found = true
			}
		}
		if !found {
			t.Errorf("%s not committed", want)
		}
	}
	if e.cm.branches[0] != "" {
		t.Errorf("triage committed to %q, want main", e.cm.branches[0])
	}
	if r := e.cm.reqs[0]; r.Stage != "triage" || r.Role != "lead" || r.JobID != j.ID || r.Summary != "Summary of the work." {
		t.Errorf("request = %+v", r)
	}
	// A second Run on the closed job changes nothing.
	calls := e.calls
	if out, err := New(e.d).Run(context.Background(), j); err != nil || out != OutcomeDone || e.calls != calls {
		t.Errorf("rerun = %s, %v, calls %d -> %d", out, err, calls, e.calls)
	}
}

func TestRunReviewChangesTargetsPR(t *testing.T) {
	e := newEnv(t)
	e.write(".ard/7/eng-review.md", roleFile("eng-review", "done", "verdict: changes"))
	j := e.claim(StageReview, "eng-review")
	out, err := New(e.d).Run(context.Background(), j)
	if err != nil || out != OutcomeChanges {
		t.Fatalf("Run = %s, %v", out, err)
	}
	l := e.labels(9)
	if !l["ard:review:eng-review:changes"] || l["ard:review:eng-review:running"] || l["ard:review:eng-review:done"] {
		t.Errorf("PR labels = %v", l)
	}
	if e.cm.branches[0] != "worker/engineer-x" {
		t.Errorf("review committed to %q", e.cm.branches[0])
	}
	if e.markerComments(9, j.ID) != 1 {
		t.Error("no comment on the PR")
	}
	if sj := e.sjob(j.ID); sj.Status != state.StatusDone {
		t.Errorf("status = %s", sj.Status)
	}
}

func TestRunWorkerDoneRequestsReviewOnPR(t *testing.T) {
	e := newEnv(t)
	e.write(".ard/7/engineer.md", roleFile("engineer", "done", "next: [review:eng-review]"))
	j := e.claim(StageWorker, "engineer")
	j.PR = e.pr
	if _, err := New(e.d).Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if l := e.labels(9); !l["ard:review:eng-review"] {
		t.Errorf("PR labels = %v", l)
	}
	if l := e.labels(7); !l["ard:worker:engineer:done"] || l["ard:worker:engineer:running"] {
		t.Errorf("issue labels = %v", l)
	}
}

func TestRunFailedAndNeedsHuman(t *testing.T) {
	t.Run("missing role file", func(t *testing.T) {
		e := newEnv(t)
		j := e.claim(StageTriage, "lead")
		out, err := New(e.d).Run(context.Background(), j)
		if err != nil || out != OutcomeFailed {
			t.Fatalf("Run = %s, %v", out, err)
		}
		l := e.labels(7)
		if !l["ard:executor:failed"] || l["ard:triage:lead:running"] || l["ard:triage:lead:done"] || !l["ard:pending"] {
			t.Errorf("labels = %v", l)
		}
		sj := e.sjob(j.ID)
		if sj.Status != state.StatusFailed || !strings.Contains(sj.FailReason, "role file missing") || sj.KeepUntil.IsZero() {
			t.Errorf("job = %+v", sj)
		}
		if len(e.cm.commits) != 1 {
			t.Errorf("commits = %d (only the log)", len(e.cm.commits))
		}
	})
	t.Run("blocked", func(t *testing.T) {
		e := newEnv(t)
		e.write(".ard/7/lead.md", roleFile("lead", "blocked", "next: [triage:architect]"))
		j := e.claim(StageTriage, "lead")
		out, err := New(e.d).Run(context.Background(), j)
		if err != nil || out != OutcomeNeedsHuman {
			t.Fatalf("Run = %s, %v", out, err)
		}
		l := e.labels(7)
		if !l["ard:needs-human"] || l["ard:triage:architect"] || !l["ard:pending"] || l["ard:triage:lead:running"] {
			t.Errorf("labels = %v", l)
		}
		cs, _ := e.gh.ListComments(context.Background(), 7, timeZero)
		if len(cs) != 1 || !strings.Contains(cs[0].Body, "· blocked**") {
			t.Errorf("comments = %+v", cs)
		}
	})
	t.Run("secret in plan.md", func(t *testing.T) {
		e := newEnv(t)
		e.write(".ard/7/lead.md", roleFile("lead", "done"))
		e.write(".ard/7/plan.md", planted())
		j := e.claim(StageTriage, "lead")
		out, err := New(e.d).Run(context.Background(), j)
		if err != nil || out != OutcomeNeedsHuman {
			t.Fatalf("Run = %s, %v", out, err)
		}
		if l := e.labels(7); !l["ard:needs-human"] || l["ard:triage:lead:done"] {
			t.Errorf("labels = %v", l)
		}
		for _, files := range e.cm.commits {
			for p, c := range files {
				if strings.Contains(c, planted()) || p == ".ard/7/plan.md" {
					t.Errorf("leaked %s", p)
				}
			}
		}
		cs, _ := e.gh.ListComments(context.Background(), 7, timeZero)
		if strings.Contains(cs[0].Body, planted()) {
			t.Error("comment leaks the secret")
		}
	})
}

func TestPermanentCommitError(t *testing.T) {
	e := newEnv(t)
	j := e.lead()
	e.cm.err = fmt.Errorf("push: %w", committer.ErrConflict)
	out, err := New(e.d).Run(context.Background(), j)
	if err != nil || out != OutcomeFailed {
		t.Fatalf("Run = %s, %v", out, err)
	}
	l := e.labels(7)
	if !l["ard:executor:failed"] || l["ard:triage:architect"] || l["ard:triage:lead:done"] || l["ard:triage:lead:running"] {
		t.Errorf("labels = %v", l)
	}
	if e.markerComments(7, j.ID) != 1 {
		t.Error("want one comment")
	}
	if sj := e.sjob(j.ID); !strings.Contains(sj.FailReason, "commit failed") {
		t.Errorf("reason = %q", sj.FailReason)
	}
}

// TestCrashMatrix interrupts the close-out at every effect, commit and
// state write in turn, then resumes with fresh collaborators.
func TestCrashMatrix(t *testing.T) {
	// A clean run counts the calls.
	probe := newEnv(t)
	j0 := probe.lead()
	if _, err := New(probe.d).Run(context.Background(), j0); err != nil {
		t.Fatal(err)
	}
	calls, saves := probe.calls, probe.fstore.n
	if calls < 6 || saves < 5 {
		t.Fatalf("calls = %d, saves = %d", calls, saves)
	}

	finish := func(t *testing.T, e *env, j Job) {
		t.Helper()
		e.fstore.failAt = 0
		cl := New(e.deps(0))
		if err := cl.Resume(context.Background()); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		// A close-out that never reached its first save is re-run by its caller.
		if out, err := cl.Run(context.Background(), j); err != nil || out != OutcomeDone {
			t.Fatalf("Run after resume = %s, %v", out, err)
		}
		e.assertLeadClosed(j)
	}
	for n := 1; n <= calls; n++ {
		t.Run(fmt.Sprintf("effect %d fails", n), func(t *testing.T) {
			e := newEnv(t)
			j := e.lead()
			if _, err := New(e.deps(n)).Run(context.Background(), j); err == nil {
				t.Fatal("want an error")
			}
			finish(t, e, j)
		})
	}
	for n := 1; n <= saves; n++ {
		t.Run(fmt.Sprintf("save %d fails after its effect", n), func(t *testing.T) {
			e := newEnv(t)
			j := e.lead()
			e.fstore.failAt = n
			if _, err := New(e.d).Run(context.Background(), j); err == nil {
				t.Fatal("want an error")
			}
			finish(t, e, j)
		})
	}
}

func TestResumeUsesStagedCopy(t *testing.T) {
	e := newEnv(t)
	j := e.lead()
	cl := New(e.d)
	rec, err := Prepare(context.Background(), j, e.d)
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.save(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	// The agent's file changes after Prepare; the worktree is even removed.
	e.write(".ard/7/lead.md", roleFile("lead", "blocked"))
	e.write(".ard/7/plan.md", "tampered")
	if err := os.RemoveAll(e.repo); err != nil {
		t.Fatal(err)
	}
	if err := cl.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.assertLeadClosed(j)
	for _, files := range e.cm.commits {
		if files[".ard/7/plan.md"] != "the plan" || !strings.Contains(files[".ard/7/lead.md"], "status: done") {
			t.Errorf("committed changed bytes: %v", files)
		}
	}
	// Run on a job that already has a record does not read the worktree.
	e2 := newEnv(t)
	j2 := e2.lead()
	rec2, _ := Prepare(context.Background(), j2, e2.d)
	cl2 := New(e2.d)
	if err := cl2.save(context.Background(), rec2); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(e2.repo); err != nil {
		t.Fatal(err)
	}
	if out, err := cl2.Run(context.Background(), j2); err != nil || out != OutcomeDone {
		t.Fatalf("Run = %s, %v", out, err)
	}
}

func TestCommentReusedWhenPresent(t *testing.T) {
	e := newEnv(t)
	j := e.lead()
	e.gh.SeedComment(7, github.Comment{ID: 4242, Body: marker(j.ID, 1) + "\nearlier"})
	if _, err := New(e.d).Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if n := e.markerComments(7, j.ID); n != 1 {
		t.Errorf("marker comments = %d", n)
	}
	rec, _ := decode(e.sjob(j.ID))
	if rec.CommentID != 4242 {
		t.Errorf("comment id = %d", rec.CommentID)
	}
}

func TestContextCancelKeepsPhase(t *testing.T) {
	e := newEnv(t)
	j := e.lead()
	e.d = e.deps(1) // the commit is the first counted call
	if _, err := New(e.d).Run(context.Background(), j); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if got := e.sjob(j.ID).CloseoutPhase; got != string(PhasePrepared) {
		t.Errorf("phase = %q, want prepared", got)
	}
}

func TestDryRun(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	dr, err := effects.NewDryRun(effects.DryRunOptions{LedgerPath: filepath.Join(dir, "effects.jsonl"), Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	defer dr.Close()
	e.d.Effects = dr
	j := e.lead()
	before := len(e.gh.Writes())
	out, err := New(e.d).Run(context.Background(), j)
	if err != nil || out != OutcomeDone {
		t.Fatalf("Run = %s, %v", out, err)
	}
	if got := len(e.gh.Writes()); got != before {
		t.Errorf("dry-run wrote to GitHub: %d writes", got-before)
	}
	if sj := e.sjob(j.ID); sj.CloseoutPhase != string(PhaseClosed) {
		t.Errorf("phase = %q", sj.CloseoutPhase)
	}
	b, err := os.ReadFile(filepath.Join(dir, "effects.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	led := string(b)
	for _, want := range []string{"CreateComment", "AddLabels", "RemoveLabel", "ard:triage:lead:done"} {
		if !strings.Contains(led, want) {
			t.Errorf("ledger lacks %s", want)
		}
	}
	if strings.Contains(led, "](https://github.com") {
		t.Error("dry-run comment has blob links")
	}
}

var timeZero time.Time
