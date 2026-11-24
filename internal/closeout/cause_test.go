package closeout

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/state"
)

// causeJob is a worker job that ended for a cause: no base commit, no
// branch, a log.
func (e *env) causeJob(stage, role string, c Cause) Job {
	e.t.Helper()
	j := e.claim(stage, role)
	j.BaseSHA, j.Worktree, j.ExitCode = "", "", -1
	j.Cause = c
	return j
}

func TestCauseFailedWithTail(t *testing.T) {
	e := newEnv(t)
	j := e.causeJob(StageTriage, "lead", Cause{Kind: CauseQuiet, Detail: "no output since 10:04 UTC"})
	var b strings.Builder
	for i := 0; i < 80; i++ {
		b.WriteString(fmt.Sprintf("step %02d\n", i))
	}
	b.WriteString("token=" + secretValue + "\n@alice <!-- ard:closeout job=X attempt=1 -->\n`````\n")
	if err := os.WriteFile(j.LogPath, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := New(e.d).Run(context.Background(), j)
	if err != nil || out != OutcomeFailed {
		t.Fatalf("Run = %s, %v", out, err)
	}
	if l := e.labels(7); !l["ard:executor:failed"] || l["ard:triage:lead:running"] {
		t.Errorf("labels = %v", l)
	}
	cs, _ := e.gh.ListComments(context.Background(), 7, time.Time{})
	if len(cs) != 1 {
		t.Fatalf("comments = %d", len(cs))
	}
	body := cs[0].Body
	for _, want := range []string{"heartbeat_quiet: no output since 10:04 UTC", "Last 50 log lines:", "Kept until", "ard:keep", "[REDACTED:"} {
		if !strings.Contains(body, want) {
			t.Errorf("comment lacks %q:\n%s", want, body)
		}
	}
	for _, bad := range []string{secretValue, "<!-- ard:closeout job=X", "@alice", "step 00", "step 28\n"} {
		if strings.Contains(body, bad) {
			t.Errorf("comment contains %q:\n%s", bad, body)
		}
	}
	if !strings.Contains(body, "``````\n") { // a run of five backticks needs a six-backtick fence
		t.Errorf("fence is not longer than the longest run:\n%s", body)
	}
	if !strings.Contains(body, "step 79") {
		t.Errorf("tail lacks the last line:\n%s", body)
	}
	if got := strings.Count(body[strings.Index(body, "Last 50"):], "\n"); got > 50+6 {
		t.Errorf("tail has %d lines", got)
	}
	sj := e.sjob(j.ID)
	if sj.Status != state.StatusFailed || !strings.HasPrefix(sj.FailReason, "heartbeat_quiet") || sj.AutoRetry {
		t.Errorf("job = %+v", sj)
	}
	if want := time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC); !sj.KeepUntil.Equal(want) {
		t.Errorf("keep_until = %v, want %v", sj.KeepUntil, want)
	}
	if len(e.cm.commits) != 1 {
		t.Errorf("commits = %d (only the log)", len(e.cm.commits))
	}
}

func TestCauseTailWithheldWhenFlagged(t *testing.T) {
	e := newEnv(t)
	j := e.causeJob(StageTriage, "lead", Cause{Kind: CauseDeadline, Detail: "ran past its deadline"})
	if err := os.WriteFile(j.LogPath, []byte("x\n"+planted()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(e.d).Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	cs, _ := e.gh.ListComments(context.Background(), 7, time.Time{})
	if strings.Contains(cs[0].Body, "Last 50 log lines") || !strings.Contains(cs[0].Body, "log withheld") ||
		!strings.Contains(cs[0].Body, "Log tail withheld") || strings.Contains(cs[0].Body, planted()) {
		t.Errorf("comment:\n%s", cs[0].Body)
	}
}

func TestCauseRetryLeavesRequestLabelAlone(t *testing.T) {
	for _, tc := range []struct {
		name    string
		present bool
	}{{"kept when present", true}, {"stays removed when a human cancelled", false}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			if tc.present {
				if err := e.gh.AddLabels(context.Background(), 7, "ard:worker:engineer"); err != nil {
					t.Fatal(err)
				}
			}
			j := e.causeJob(StageWorker, "engineer", Cause{Kind: CauseRateLimit, Detail: "x", Transient: true, Retry: true})
			if out, err := New(e.d).Run(context.Background(), j); err != nil || out != OutcomeRetry {
				t.Fatalf("Run = %s, %v", out, err)
			}
			l := e.labels(7)
			if l["ard:worker:engineer"] != tc.present || l["ard:worker:engineer:running"] || l["ard:executor:failed"] {
				t.Errorf("labels = %v", l)
			}
		})
	}
}

func TestCauseRetryHandsOff(t *testing.T) {
	e := newEnv(t)
	j := e.causeJob(StageWorker, "engineer", Cause{Kind: CauseRateLimit, Detail: "API status 429", Transient: true, Retry: true})
	out, err := New(e.d).Run(context.Background(), j)
	if err != nil || out != OutcomeRetry {
		t.Fatalf("Run = %s, %v", out, err)
	}
	l := e.labels(7)
	if l["ard:executor:failed"] || l["ard:worker:engineer:running"] {
		t.Errorf("labels = %v", l)
	}
	sj := e.sjob(j.ID)
	if sj.Status != state.StatusFailed || !sj.AutoRetry || sj.Attempt != 1 {
		t.Errorf("job = %+v", sj)
	}
	cs, _ := e.gh.ListComments(context.Background(), 7, time.Time{})
	if len(cs) != 1 || !strings.Contains(cs[0].Body, "retrying") || !strings.Contains(cs[0].Body, "Retrying once as a new job.") {
		t.Errorf("comments = %+v", cs)
	}
	// The next claim is the linked retry.
	next, err := e.store.CreateJob(context.Background(), state.NewJob{Stage: StageWorker, Role: "engineer", Issue: 7})
	if err != nil || next.RetryOf != j.ID {
		t.Errorf("next = %+v, %v", next, err)
	}
}

// A worker that stopped before its first push has no branch: the log cannot be
// committed, and that must not turn a retry into a failure.
func TestCauseRetryCommitFailureKeepsOutcome(t *testing.T) {
	e := newEnv(t)
	e.cm.err = committer.ErrBranchGone
	j := e.causeJob(StageWorker, "engineer", Cause{Kind: CauseRestart, Detail: "orphaned", Transient: true, Retry: true})
	out, err := New(e.d).Run(context.Background(), j)
	if err != nil || out != OutcomeRetry {
		t.Fatalf("Run = %s, %v", out, err)
	}
	if l := e.labels(7); l["ard:executor:failed"] {
		t.Errorf("labels = %v", l)
	}
	cs, _ := e.gh.ListComments(context.Background(), 7, time.Time{})
	if len(cs) != 1 || !strings.Contains(cs[0].Body, "log not committed") {
		t.Errorf("comments = %+v", cs)
	}
}

func TestCauseNoBranchCommitsToMain(t *testing.T) {
	e := newEnv(t)
	j := e.causeJob(StageWorker, "engineer", Cause{Kind: CauseNoProcess})
	j.Branch = ""
	if _, err := New(e.d).Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if len(e.cm.branches) != 1 || e.cm.branches[0] != "" {
		t.Errorf("branches = %q", e.cm.branches)
	}
}

func TestValidateWithoutCause(t *testing.T) {
	j := Job{ID: "J", Stage: StageWorker, Role: "r", Issue: 1}
	if err := j.validate(); err == nil {
		t.Error("a worker job with no cause, base or branch is valid")
	}
	j.Cause = Cause{Kind: CauseNoProcess}
	if err := j.validate(); err != nil {
		t.Errorf("with a cause: %v", err)
	}
}

func TestResumeJobAndBusy(t *testing.T) {
	e := newEnv(t)
	c := New(e.d)
	if _, err := c.ResumeJob(context.Background(), "nope"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("unknown job: %v", err)
	}
	j := e.causeJob(StageTriage, "lead", Cause{Kind: CauseNoProcess})
	if _, err := c.ResumeJob(context.Background(), j.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("no open phase: %v", err)
	}
	// Crash after the log commit: the job is left in phase committed.
	e.d = e.deps(0)
	e.fstore.failAt = 3 // prepared, committed, then fail while saving "requested"
	c = New(e.d)
	if _, err := c.Run(context.Background(), j); err == nil {
		t.Fatal("expected the injected store failure")
	}
	e.fstore.failAt = 0
	sj := e.sjob(j.ID)
	if sj.CloseoutPhase != string(PhaseCommitted) {
		t.Fatalf("phase = %q", sj.CloseoutPhase)
	}
	if !c.acquire(j.ID) {
		t.Fatal("acquire")
	}
	if _, err := c.ResumeJob(context.Background(), j.ID); !errors.Is(err, ErrBusy) {
		t.Errorf("held lock: %v, want ErrBusy", err)
	}
	if _, err := c.Run(context.Background(), j); !errors.Is(err, ErrBusy) {
		t.Errorf("Run on held lock: %v, want ErrBusy", err)
	}
	c.release(j.ID)
	out, err := c.ResumeJob(context.Background(), j.ID)
	if err != nil || out != OutcomeFailed {
		t.Fatalf("ResumeJob = %s, %v", out, err)
	}
	if e.markerComments(7, j.ID) != 1 {
		t.Error("want exactly one comment")
	}
	if l := e.labels(7); !l["ard:executor:failed"] || l["ard:triage:lead:running"] {
		t.Errorf("labels = %v", l)
	}
}

var limitReset = time.Date(2026, 11, 22, 15, 45, 0, 0, time.UTC)

func limitCause() Cause {
	return Cause{Kind: CauseUsageLimit, Detail: "session limit", Transient: true, Family: "", ResetsAt: limitReset}
}

// A usage-limit stop is not a failure: only :running comes off, the request
// label stays, and the job keeps its retry budget.
func TestCauseUsageLimit(t *testing.T) {
	e := newEnv(t)
	if err := e.gh.AddLabels(context.Background(), 7, "ard:worker:engineer"); err != nil {
		t.Fatal(err)
	}
	j := e.causeJob(StageWorker, "engineer", limitCause())
	out, err := New(e.d).Run(context.Background(), j)
	if err != nil || out != OutcomeLimited {
		t.Fatalf("Run = %s, %v", out, err)
	}
	if l := e.labels(7); !l["ard:worker:engineer"] || l["ard:worker:engineer:running"] || l["ard:executor:failed"] || l["ard:needs-human"] {
		t.Errorf("labels = %v", l)
	}
	sj := e.sjob(j.ID)
	if sj.Status != state.StatusFailed || !sj.LimitStop || sj.AutoRetry {
		t.Errorf("job = %+v", sj)
	}
	if evs, _ := e.store.Events(context.Background(), state.EventFilter{Kind: state.EventUsageLimit}); len(evs) != 1 {
		t.Errorf("usage_limit events = %d", len(evs))
	}
	cs, _ := e.gh.ListComments(context.Background(), 7, time.Time{})
	if len(cs) != 1 {
		t.Fatalf("comments = %d", len(cs))
	}
	for _, want := range []string{"usage limit", "Paused for usage limit until 2026-11-22 15:45 UTC (account)", "re-runs after the reset", "Kept until"} {
		if !strings.Contains(cs[0].Body, want) {
			t.Errorf("comment lacks %q:\n%s", want, cs[0].Body)
		}
	}
	next, err := e.store.CreateJob(context.Background(), state.NewJob{Stage: StageWorker, Role: "engineer", Issue: 7})
	if err != nil || next.AfterLimit != j.ID || next.RetryOf != "" {
		t.Errorf("next = %+v, %v", next, err)
	}
}

// The limit plan wins over a Retry flag: a limit never hands off to the retry.
func TestCauseUsageLimitIgnoresRetryFlag(t *testing.T) {
	e := newEnv(t)
	c := limitCause()
	c.Retry = true
	j := e.causeJob(StageWorker, "engineer", c)
	out, err := New(e.d).Run(context.Background(), j)
	if err != nil || out != OutcomeLimited {
		t.Fatalf("Run = %s, %v", out, err)
	}
	if sj := e.sjob(j.ID); sj.AutoRetry {
		t.Errorf("job = %+v", sj)
	}
}

func TestLimitedPlanLabels(t *testing.T) {
	j := Job{ID: "J", Issue: 7, Stage: StageWorker, Role: "engineer"}
	p := limitedPlan(j, "usage_limit: weekly limit")
	if p.Outcome != OutcomeLimited || len(p.Terminal) != 0 || len(p.Requests) != 0 ||
		len(p.Remove) != 1 || p.Remove[0] != (Label{7, "ard:worker:engineer:running"}) {
		t.Errorf("plan = %+v", p)
	}
}

// A worker that hit a limit before its first push has no branch to commit the
// log to; the limit stop must stay a limit stop.
func TestCauseUsageLimitCommitFailureKeepsOutcome(t *testing.T) {
	e := newEnv(t)
	e.cm.err = committer.ErrBranchGone
	j := e.causeJob(StageWorker, "engineer", limitCause())
	out, err := New(e.d).Run(context.Background(), j)
	if err != nil || out != OutcomeLimited {
		t.Fatalf("Run = %s, %v", out, err)
	}
	if l := e.labels(7); l["ard:executor:failed"] {
		t.Errorf("labels = %v", l)
	}
	cs, _ := e.gh.ListComments(context.Background(), 7, time.Time{})
	if len(cs) != 1 || !strings.Contains(cs[0].Body, "log not committed") || !strings.Contains(cs[0].Body, "Paused for usage limit") {
		t.Errorf("comments = %+v", cs)
	}
}

// A record resumed from phase committed finishes with exactly one comment.
func TestCauseUsageLimitResume(t *testing.T) {
	e := newEnv(t)
	j := e.causeJob(StageTriage, "lead", limitCause())
	e.d = e.deps(0)
	e.fstore.failAt = 3
	c := New(e.d)
	if _, err := c.Run(context.Background(), j); err == nil {
		t.Fatal("expected the injected store failure")
	}
	e.fstore.failAt = 0
	if sj := e.sjob(j.ID); sj.CloseoutPhase != string(PhaseCommitted) {
		t.Fatalf("phase = %q", sj.CloseoutPhase)
	}
	out, err := c.ResumeJob(context.Background(), j.ID)
	if err != nil || out != OutcomeLimited {
		t.Fatalf("ResumeJob = %s, %v", out, err)
	}
	if e.markerComments(7, j.ID) != 1 {
		t.Error("want exactly one comment")
	}
	// The resumed record still carries the reset time.
	rec, err := decode(e.sjob(j.ID))
	if err != nil || !rec.Job.Cause.ResetsAt.Equal(limitReset) {
		t.Errorf("record = %+v, %v", rec.Job.Cause, err)
	}
}

// The unreported-tokens mark is stored with the record: a resumed close-out
// shows it once, and FactsOf carries it to the cost table.
func TestTokensUnreportedMarkOnceOnResumeAndFacts(t *testing.T) {
	e := newEnv(t)
	j := e.causeJob(StageTriage, "lead", Cause{Kind: CauseNoProcess})
	j.TokensUnreported = true
	e.d = e.deps(0)
	e.fstore.failAt = 3
	c := New(e.d)
	if _, err := c.Run(context.Background(), j); err == nil {
		t.Fatal("expected the injected store failure")
	}
	e.fstore.failAt = 0
	if _, err := c.ResumeJob(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	cs, _ := e.gh.ListComments(context.Background(), 7, time.Time{})
	n := 0
	for _, cm := range cs {
		if strings.HasPrefix(cm.Body, markerFor(j.ID)) {
			n++
			if got := strings.Count(cm.Body, "Tokens not reported"); got != 1 {
				t.Errorf("mark count = %d in\n%s", got, cm.Body)
			}
		}
	}
	if n != 1 {
		t.Errorf("marker comments = %d, want 1", n)
	}
	f, err := FactsOf(e.sjob(j.ID))
	if err != nil || !f.TokensUnreported {
		t.Errorf("facts = %+v, %v", f, err)
	}
	// Without the flag the facts carry no mark.
	j2 := e.causeJob(StageTriage, "lead", Cause{Kind: CauseNoProcess})
	if _, err := New(e.deps(0)).Run(context.Background(), j2); err != nil {
		t.Fatal(err)
	}
	if f, err := FactsOf(e.sjob(j2.ID)); err != nil || f.TokensUnreported {
		t.Errorf("facts = %+v, %v", f, err)
	}
}
