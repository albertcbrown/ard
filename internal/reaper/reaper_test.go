package reaper

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

var (
	_ controller.Loop          = (*Reaper)(nil)
	_ controller.TickTimeouter = (*Reaper)(nil)
)

func TestLoopShape(t *testing.T) {
	r := New(Deps{})
	if r.Name() != "reaper" || r.Claims() || r.Interval(nil) != time.Minute || r.TickTimeout(nil) < time.Minute {
		t.Errorf("loop = %s claims=%v interval=%v timeout=%v", r.Name(), r.Claims(), r.Interval(nil), r.TickTimeout(nil))
	}
}

// markerCount counts the close-out comments of job id on issue n.
func (h *harness) markerCount(n int, id string) int {
	c := 0
	for _, cm := range h.comments(n) {
		if strings.HasPrefix(cm.Body, "<!-- ard:closeout job="+id+" ") {
			c++
		}
	}
	return c
}

// checkEvidence asserts what every Reaper failure leaves behind.
func (h *harness) checkEvidence(sj state.Job, wantReason string) {
	t := h.t
	t.Helper()
	noLog := h.job(sj.ID).Handle == nil
	cs := h.comments(sj.Issue)
	if len(cs) != 1 || h.markerCount(sj.Issue, sj.ID) != 1 {
		t.Fatalf("comments = %d, want exactly one close-out comment", len(cs))
	}
	body := cs[0].Body
	if !strings.Contains(body, wantReason) {
		t.Errorf("comment lacks reason %q:\n%s", wantReason, body)
	}
	if noLog { // never started: there is no log to tail or commit
		if strings.Contains(body, "Last 50") || len(h.cm.reqs) != 0 || !strings.Contains(body, "Kept until") {
			t.Errorf("a job with no log:\n%s", body)
		}
		return
	}
	i := strings.Index(body, "Last 50 log lines:")
	if i < 0 {
		t.Fatalf("no tail:\n%s", body)
	}
	if !strings.Contains(body, "Kept until 2026-") || !strings.Contains(body, "step 59") || strings.Contains(body, "step 09\n") {
		t.Errorf("comment lacks a 50-line tail or the kept-until line:\n%s", body)
	}
	if n := strings.Count(body[i:], "\n"); n > 50+4 {
		t.Errorf("tail has %d lines", n)
	}
	if strings.Contains(body, secretValue) || strings.Contains(body, h.dir) {
		t.Errorf("comment leaks a secret or a host path:\n%s", body)
	}
	if len(h.cm.reqs) == 0 {
		t.Fatal("no commit")
	}
	last := h.cm.reqs[len(h.cm.reqs)-1]
	if len(last.Files) != 1 || !strings.HasPrefix(last.Files[0].Path, ".ard/"+itoa(sj.Issue)+"/logs/") {
		t.Errorf("commit files = %+v, want only the log under .ard/%d/logs/", last.Files, sj.Issue)
	}
	for _, c := range h.ex.cleanupsOf(sj.ID) {
		if !c.Keep {
			t.Errorf("cleanup removed the worktree of a failed job: %+v", c)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestEveryCauseEndToEnd(t *testing.T) {
	type tc struct {
		name    string
		setup   func(h *harness) state.Job
		retry   bool // outcome retry; else failed
		reason  string
		release bool // an adopted slot must be freed
	}
	gone := executor.Status{State: executor.Gone}
	tests := []tc{
		{"deadline", func(h *harness) state.Job {
			sj, _ := h.startJob(11, withStatus(executor.Status{State: executor.TimedOut, ExitCode: -1, Reason: "deadline exceeded"}))
			h.adopt()
			return sj
		}, false, "deadline: ran past its deadline", true},
		{"gone in this run", func(h *harness) state.Job {
			sj, _ := h.startJob(12, withStatus(gone))
			h.adopt()
			return sj
		}, false, "no_process: process gone without a result", true},
		{"gone across a restart", func(h *harness) state.Job {
			sj, _ := h.startJob(13, withStatus(gone))
			h.restart()
			return sj
		}, true, "controller_restart: process gone after a controller restart", true},
		{"orphaned", func(h *harness) state.Job {
			sj, _ := h.startJob(14, withStatus(executor.Status{State: executor.Orphaned, Alive: true}))
			h.restart()
			return sj
		}, true, "controller_restart: orphaned by a controller restart", true},
		{"never started across a restart", func(h *harness) state.Job {
			sj, _ := h.startJob(15, noHandle)
			h.restart()
			return sj
		}, true, "controller_restart: never started before a controller restart", true},
		{"never started, no driver", func(h *harness) state.Job {
			sj, _ := h.startJob(16, noHandle)
			return sj // no slot at all
		}, false, "no_process: never started and its driver is gone", false},
		{"running but the driver released the slot", func(h *harness) state.Job {
			sj, _ := h.startJob(17)
			return sj
		}, false, "no_process: its driver released the job while it was running", false},
		{"rate limit", func(h *harness) state.Job {
			sj, _ := h.startJob(18, withStatus(executor.Status{State: executor.Exited, ExitCode: 1}))
			h.results[sj.ID] = agent.Result{APIErrorStatus: 429}
			h.adopt()
			return sj
		}, true, "rate_limit: API status 429", true},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			sj := c.setup(h)
			h.mustTick()
			got := h.job(sj.ID)
			l := h.labels(sj.Issue)
			if got.Status != state.StatusFailed || l[labels.Running(labels.StageWorker, "engineer")] {
				t.Fatalf("job = %+v labels = %v", got, l)
			}
			if c.retry {
				if l[labels.ExecutorFailed] || !l[labels.Request(labels.StageWorker, "engineer")] || !got.AutoRetry {
					t.Errorf("retry: labels = %v auto_retry = %v", l, got.AutoRetry)
				}
			} else if !l[labels.ExecutorFailed] || got.AutoRetry {
				t.Errorf("failed: labels = %v auto_retry = %v", l, got.AutoRetry)
			}
			if !strings.HasPrefix(got.FailReason, strings.SplitN(c.reason, ":", 2)[0]) {
				t.Errorf("fail reason = %q, want prefix of %q", got.FailReason, c.reason)
			}
			if want := h.clk.t.Add(24 * time.Hour); !got.KeepUntil.Equal(want) {
				t.Errorf("keep_until = %v, want %v", got.KeepUntil, want)
			}
			h.checkEvidence(sj, c.reason)
			atts, _ := h.store.Attempts(context.Background(), sj.ID)
			if len(atts) != 1 || atts[0].EndedAt.IsZero() || atts[0].Cause != strings.SplitN(c.reason, ":", 2)[0] {
				t.Errorf("attempts = %+v", atts)
			}
			if c.release {
				ds := h.pool.Offer("w", time.Minute, []pool.Candidate{candidateOf(sj)})
				if ds[0].Verdict != pool.Granted {
					t.Errorf("adopted slot not released: %v", ds[0].Verdict)
				}
			}
			// A second tick changes nothing.
			before := h.writes()
			h.mustTick()
			if h.writes() != before || h.markerCount(sj.Issue, sj.ID) != 1 {
				t.Errorf("second tick wrote %d more calls", h.writes()-before)
			}
		})
	}
}

// A job whose agent ended normally with a role file is closed out from it,
// not failed: Exited with OK and no cause is the zero Cause path.
func TestExitedNormallyIsNotACause(t *testing.T) {
	h := newHarness(t)
	sj, _ := h.startJob(21, withStatus(executor.Status{State: executor.Exited}))
	h.results[sj.ID] = agent.Result{OK: true}
	h.adopt()
	h.mustTick()
	// No role file in the (empty) worktree: close-out fails it with its own
	// reason, not one of the Reaper's.
	got := h.job(sj.ID)
	if got.Status != state.StatusFailed || !strings.HasPrefix(got.FailReason, "role file missing") {
		t.Errorf("job = %+v", got)
	}
}

func TestOwnedQuietKillThenOwnerFinishes(t *testing.T) {
	h := newHarness(t)
	sj, hd := h.startJob(31)
	slot := h.own(sj)
	// Silent for 11 minutes.
	st := h.ex.st[sj.ID]
	st.LastOutput = h.clk.t
	h.ex.st[sj.ID] = st
	h.clk.add(11 * time.Minute)
	before := h.writes()
	h.mustTick()
	if h.writes() != before {
		t.Errorf("the Reaper wrote %d GitHub calls itself", h.writes()-before)
	}
	got := h.job(sj.ID)
	if got.Status != state.StatusRunning || got.KillReason != "heartbeat_quiet" {
		t.Fatalf("job = %+v", got)
	}
	if cs := h.ex.cleanupsOf(sj.ID); len(cs) != 1 || !cs[0].Keep {
		t.Errorf("cleanups = %+v", cs)
	}
	// The owner holds the stale claim-time job and sees Gone, because the kill
	// dropped the tracker.
	stGone, _ := h.ex.Status(context.Background(), hd)
	if stGone.State != executor.Gone {
		t.Fatalf("status after kill = %v", stGone.State)
	}
	out, err := h.fin.Finish(context.Background(), sj.ID, hd, stGone, agent.Result{}, closeout.Cause{})
	if err != nil || out != closeout.OutcomeFailed {
		t.Fatalf("Finish = %s, %v", out, err)
	}
	got = h.job(sj.ID)
	if got.AutoRetry || !strings.HasPrefix(got.FailReason, "heartbeat_quiet") || strings.Contains(got.FailReason, "agent exited") {
		t.Errorf("job = %+v", got)
	}
	if l := h.labels(31); !l[labels.ExecutorFailed] || l[labels.Running(labels.StageWorker, "engineer")] {
		t.Errorf("labels = %v", l)
	}
	h.checkEvidence(sj, "heartbeat_quiet: no output since 03:04 UTC")
	slot.Release()
	// Tidy kept the worktree.
	if _, err := os.Stat(hd.Worktree); err != nil {
		t.Errorf("worktree of a failed job was removed: %v", err)
	}
}

func TestOwnedDeadlineKill(t *testing.T) {
	h := newHarness(t)
	sj, _ := h.startJob(32, withDeadline(2*time.Minute))
	h.own(sj)
	// Output keeps coming, so the heartbeat is fine; only the deadline passed.
	h.clk.add(9 * time.Minute)
	st := h.ex.st[sj.ID]
	st.LastOutput = h.clk.t
	h.ex.st[sj.ID] = st
	h.mustTick()
	if got := h.job(sj.ID); got.KillReason != "deadline" {
		t.Errorf("kill reason = %q", got.KillReason)
	}
}

func TestOwnedHealthyJobIsLeftAlone(t *testing.T) {
	h := newHarness(t)
	sj, _ := h.startJob(33)
	h.own(sj)
	h.clk.add(5 * time.Minute)
	h.mustTick()
	if got := h.job(sj.ID); got.KillReason != "" || len(h.ex.cleanups) != 0 {
		t.Errorf("healthy job touched: %+v %v", got, h.ex.cleanups)
	}
}

// A slot that belongs to a different job id is not this job's owner.
func TestSlotOfAnotherJobIsNotOwnership(t *testing.T) {
	h := newHarness(t)
	sj, _ := h.startJob(34, withStatus(executor.Status{State: executor.Gone}))
	s := h.own(sj)
	s.SetJobID("SOMEONE-ELSE")
	h.mustTick()
	if got := h.job(sj.ID); got.Status != state.StatusFailed {
		t.Errorf("job = %+v", got)
	}
}

func TestKillFailureKeepsReasonAndRetries(t *testing.T) {
	h := newHarness(t)
	sj, _ := h.startJob(35)
	h.own(sj)
	h.clk.add(11 * time.Minute)
	h.ex.err = errBoom
	if err := h.tick(); !errors.Is(err, errBoom) {
		t.Fatalf("tick = %v, want the kill error", err)
	}
	if got := h.job(sj.ID); got.KillReason != "heartbeat_quiet" || got.Status != state.StatusRunning {
		t.Errorf("job = %+v", got)
	}
	h.ex.err = nil
	h.mustTick()
	if cs := h.ex.cleanupsOf(sj.ID); len(cs) != 1 {
		t.Errorf("cleanups after retry = %+v", cs)
	}
}

// One bad job must not stop the others.
func TestOneBadJobDoesNotStopTheRest(t *testing.T) {
	h := newHarness(t)
	bad, _ := h.startJob(36, withStatus(executor.Status{State: executor.Gone}))
	good, _ := h.startJob(37, withStatus(executor.Status{State: executor.Gone}))
	h.adopt()
	h.gh.FailNext("CreateComment", errBoom)
	if err := h.tick(); err == nil {
		t.Fatal("want the injected comment failure")
	}
	if h.job(bad.ID).Status != state.StatusRunning || h.job(good.ID).Status != state.StatusFailed {
		t.Errorf("bad = %v good = %v", h.job(bad.ID).Status, h.job(good.ID).Status)
	}
	// The failed close-out is resumable: the next tick finishes it.
	h.mustTick()
	if got := h.job(bad.ID); got.Status != state.StatusFailed || h.markerCount(36, bad.ID) != 1 {
		t.Errorf("bad after resume = %+v, comments %d", got, h.markerCount(36, bad.ID))
	}
}

func TestScannerJobsAreSkipped(t *testing.T) {
	h := newHarness(t)
	sj, err := h.store.CreateJob(context.Background(), state.NewJob{Stage: "scanner", Role: "cve"})
	if err != nil {
		t.Fatal(err)
	}
	h.mustTick()
	if h.job(sj.ID).Status != state.StatusRunning || h.writes() != 0 {
		t.Error("scanner job was touched")
	}
}

func TestCommitFailureOnRetryKeepsOutcome(t *testing.T) {
	h := newHarness(t)
	h.cm.err = committer.ErrBranchGone
	sj, _ := h.startJob(41, withStatus(executor.Status{State: executor.Gone}))
	h.restart()
	h.mustTick()
	if l := h.labels(41); l[labels.ExecutorFailed] || !l[labels.Request(labels.StageWorker, "engineer")] {
		t.Errorf("labels = %v", l)
	}
	cs := h.comments(41)
	if len(cs) != 1 || !strings.Contains(cs[0].Body, "log not committed") || !strings.Contains(cs[0].Body, "retrying") {
		t.Errorf("comments = %+v", cs)
	}
	if got := h.job(sj.ID); !got.AutoRetry {
		t.Errorf("job = %+v", got)
	}
}

// --- startup and resume ---

// crashedCloseout leaves job sj with an open close-out: the comment failed.
func (h *harness) crashedCloseout(sj state.Job, hd executor.Handle, st executor.Status) {
	h.t.Helper()
	h.gh.FailNext("CreateComment", errBoom)
	if _, err := h.fin.Finish(context.Background(), sj.ID, hd, st, agent.Result{}, closeout.Cause{}); err == nil {
		h.t.Fatal("want the injected failure")
	}
	if p := h.job(sj.ID).CloseoutPhase; p == "" {
		h.t.Fatalf("phase = %q", p)
	}
}

func TestResumeAllSurvivesGitHubError(t *testing.T) {
	h := newHarness(t)
	sj, hd := h.startJob(51, withStatus(executor.Status{State: executor.Gone}))
	h.crashedCloseout(sj, hd, executor.Status{State: executor.Gone})
	// Restart; the 502 persists during startup.
	h.restart()
	h.gh.FailNext("CreateComment", errBoom)
	if err := h.r.ResumeAll(context.Background()); err != nil {
		t.Fatalf("ResumeAll = %v, want nil: a 502 must not keep serve down", err)
	}
	if got := h.job(sj.ID); got.Status != state.StatusRunning {
		t.Fatalf("job = %+v, must stay running", got)
	}
	if s, ok := h.pool.Lookup(candidateOf(sj).Key()); !ok || !s.Adopted() {
		t.Error("the job must be adopted")
	}
	// The next tick resumes it, once, and frees the slot.
	h.mustTick()
	got := h.job(sj.ID)
	if got.Status != state.StatusFailed || h.markerCount(51, sj.ID) != 1 {
		t.Errorf("job = %+v comments = %d", got, h.markerCount(51, sj.ID))
	}
	if _, ok := h.pool.Lookup(candidateOf(sj).Key()); ok {
		t.Error("slot not released")
	}
}

func TestResumeAllFinishesCloseouts(t *testing.T) {
	h := newHarness(t)
	sj, hd := h.startJob(52, withStatus(executor.Status{State: executor.Gone}))
	h.crashedCloseout(sj, hd, executor.Status{State: executor.Gone})
	h.restart()
	if err := h.r.ResumeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.job(sj.ID); got.Status != state.StatusFailed || h.markerCount(52, sj.ID) != 1 {
		t.Errorf("job = %+v", got)
	}
}

// closeoutRecord stores a hand-made close-out record in phase committed: the
// agent finished and the log is committed; the comment and labels are left.
func (h *harness) committedDone(sj state.Job, hd executor.Handle) {
	h.t.Helper()
	st := labels.StageWorker
	rec := closeout.Record{Version: 1, Phase: closeout.PhaseCommitted,
		Job: closeout.Job{ID: sj.ID, Issue: sj.Issue, Stage: sj.Stage, Role: sj.Role, Attempt: 1, Branch: sj.Branch, Worktree: hd.Worktree},
		Plan: closeout.Plan{Outcome: closeout.OutcomeDone, Status: "done", Summary: "all done",
			Terminal: []closeout.Label{{Number: sj.Issue, Name: labels.Done(st, sj.Role)}},
			Remove:   []closeout.Label{{Number: sj.Issue, Name: labels.Running(st, sj.Role)}}}}
	raw, err := jsonMarshal(rec)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.SetCloseout(context.Background(), sj.ID, string(rec.Phase), raw); err != nil {
		h.t.Fatal(err)
	}
}

func TestResumeNotFail(t *testing.T) {
	h := newHarness(t)
	sj, hd := h.startJob(61, withStatus(executor.Status{State: executor.Exited}))
	h.committedDone(sj, hd)
	h.adopt()
	h.mustTick()
	l := h.labels(61)
	if l[labels.ExecutorFailed] || !l[labels.Done(labels.StageWorker, "engineer")] || l[labels.Running(labels.StageWorker, "engineer")] {
		t.Errorf("labels = %v", l)
	}
	if got := h.job(sj.ID); got.Status != state.StatusDone || h.markerCount(61, sj.ID) != 1 {
		t.Errorf("job = %+v", got)
	}
	if _, ok := h.pool.Lookup(candidateOf(sj).Key()); ok {
		t.Error("adopted slot not released")
	}
	// A done job's worktree is removed and the job marked cleaned.
	if cs := h.ex.cleanupsOf(sj.ID); len(cs) != 1 || cs[0].Keep {
		t.Errorf("cleanups = %+v", cs)
	}
	if h.job(sj.ID).CleanedAt.IsZero() {
		t.Error("done job not marked cleaned")
	}
}

func TestOwnedOpenCloseoutAndBusyAreSkipped(t *testing.T) {
	h := newHarness(t)
	owned, ohd := h.startJob(62, withStatus(executor.Status{State: executor.Exited}))
	h.committedDone(owned, ohd)
	h.own(owned)
	busy, bhd := h.startJob(63, withStatus(executor.Status{State: executor.Exited}))
	h.committedDone(busy, bhd)
	h.busy[busy.ID] = true
	h.mustTick()
	if h.job(owned.ID).Status != state.StatusRunning || h.job(busy.ID).Status != state.StatusRunning || h.writes() != 0 {
		t.Error("an owned or busy close-out was touched")
	}
}

func TestRestartFreesSlotForNewOffer(t *testing.T) {
	h := newHarness(t)
	sj, _ := h.startJob(71, withStatus(executor.Status{State: executor.Gone}))
	h.restart()
	c := candidateOf(sj)
	if ds := h.pool.Offer("w", time.Minute, []pool.Candidate{c}); ds[0].Verdict != pool.DeniedDuplicate {
		t.Fatalf("before the tick: %v", ds[0].Verdict)
	}
	h.mustTick()
	if ds := h.pool.Offer("w", time.Minute, []pool.Candidate{c}); ds[0].Verdict != pool.Granted {
		t.Errorf("after the tick: %v", ds[0].Verdict)
	}
}

// --- retries ---

func TestAutomaticRetryIsANewLinkedJob(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	first, _ := h.startJob(81, withStatus(executor.Status{State: executor.Exited, ExitCode: 1}))
	h.results[first.ID] = agent.Result{APIErrorStatus: 429}
	h.adopt()
	h.mustTick()
	got := h.job(first.ID)
	if !got.AutoRetry || got.Status != state.StatusFailed {
		t.Fatalf("first = %+v", got)
	}
	if evs, _ := h.store.Events(ctx, state.EventFilter{Kind: state.EventRetry}); len(evs) != 1 || evs[0].JobID != first.ID {
		t.Errorf("retry events = %+v", evs)
	}
	// The loop claims again: a new job, attempt 1, linked.
	second, _ := h.startJob(81, withStatus(executor.Status{State: executor.Exited, ExitCode: 1}))
	if second.ID == first.ID || second.RetryOf != first.ID {
		t.Fatalf("second = %+v", second)
	}
	if got := h.job(second.ID); got.Attempt != 1 {
		t.Errorf("attempt = %d, want 1", got.Attempt)
	}
	// It fails the same way: no second retry, ard:executor:failed.
	h.results[second.ID] = agent.Result{APIErrorStatus: 429}
	h.adopt()
	h.mustTick()
	got = h.job(second.ID)
	if got.AutoRetry || got.Status != state.StatusFailed || !h.labels(81)[labels.ExecutorFailed] {
		t.Errorf("second = %+v labels = %v", got, h.labels(81))
	}
	// A human retry after that failure is unlinked.
	third, _ := h.startJob(81)
	if third.RetryOf != "" {
		t.Errorf("human retry linked: %+v", third)
	}
}

func TestClassifyHookOverridesCause(t *testing.T) {
	h := newHarness(t)
	h.classify = func(res agent.Result) (closeout.Cause, bool) {
		if res.Subtype == "usage_limit" {
			return closeout.Cause{Kind: closeout.CauseRateLimit, Detail: "usage limit", Transient: true}, true
		}
		return closeout.Cause{}, false
	}
	sj, _ := h.startJob(91, withStatus(executor.Status{State: executor.Exited, ExitCode: 1}))
	h.results[sj.ID] = agent.Result{Subtype: "usage_limit", APIErrorStatus: 429}
	h.adopt()
	h.mustTick()
	// Transient but Retry false: it neither retried nor used up the retry.
	got := h.job(sj.ID)
	if got.AutoRetry || !h.labels(91)[labels.ExecutorFailed] || !strings.HasPrefix(got.FailReason, "rate_limit: usage limit") {
		t.Errorf("job = %+v labels = %v", got, h.labels(91))
	}
}

func TestFinishOverrideCloneFailed(t *testing.T) {
	h := newHarness(t)
	sj, hd := h.startJob(92, noHandle)
	hd.PID = 0
	cause := NewCause(sj, closeout.CauseCloneFail, "git fetch failed", true)
	out, err := h.fin.Finish(context.Background(), sj.ID, hd, executor.Status{State: executor.Prepared}, agent.Result{}, cause)
	if err != nil || out != closeout.OutcomeRetry {
		t.Fatalf("Finish = %s, %v", out, err)
	}
	if l := h.labels(92); l[labels.ExecutorFailed] || !l[labels.Request(labels.StageWorker, "engineer")] {
		t.Errorf("labels = %v", l)
	}
}

// --- retention, prune ---

func TestRetention(t *testing.T) {
	type tc struct {
		name       string
		age        time.Duration // time since the job failed
		keep       bool          // ard:keep on the issue
		getErr     error
		wantClean  bool
		wantExtend bool
	}
	tests := []tc{
		{"not expired", 24*time.Hour - time.Minute, false, nil, false, false},
		{"expired", 24 * time.Hour, false, nil, true, false},
		{"ard:keep extends", 25 * time.Hour, true, nil, false, true},
		{"github error fails closed", 25 * time.Hour, false, errBoom, false, false},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			sj, hd := h.startJob(101, withStatus(executor.Status{State: executor.Gone}))
			h.adopt()
			h.mustTick() // fails it
			if h.job(sj.ID).Status != state.StatusFailed {
				t.Fatal("setup: not failed")
			}
			// Close-out drops the raw log once its redacted copy is committed; a
			// log kept because redaction failed is what retention must delete.
			if err := os.WriteFile(hd.LogPath, []byte("kept raw log\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if c.keep {
				_ = h.gh.AddLabels(context.Background(), 101, labels.Keep)
			}
			h.clk.add(c.age)
			if c.getErr != nil {
				h.gh.FailNext("GetIssue", c.getErr)
			}
			err := h.tick()
			if (err != nil) != (c.getErr != nil) {
				t.Errorf("tick = %v", err)
			}
			got := h.job(sj.ID)
			if cleaned := !got.CleanedAt.IsZero(); cleaned != c.wantClean {
				t.Errorf("cleaned = %v, want %v", cleaned, c.wantClean)
			}
			_, wtErr := os.Stat(hd.Worktree)
			_, logErr := os.Stat(hd.LogPath)
			_, outErr := os.Stat(hd.StdoutPath)
			if c.wantClean {
				if wtErr == nil || logErr == nil || outErr == nil {
					t.Errorf("expected worktree, log and stdout removed: %v %v %v", wtErr, logErr, outErr)
				}
				last := h.ex.cleanupsOf(sj.ID)
				if len(last) == 0 || last[len(last)-1].Keep {
					t.Errorf("final cleanup = %+v", last)
				}
			} else if wtErr != nil || logErr != nil || outErr != nil {
				t.Errorf("evidence deleted early: %v %v %v", wtErr, logErr, outErr)
			}
			if c.wantExtend {
				if want := h.clk.t.Add(24 * time.Hour); !got.KeepUntil.Equal(want) {
					t.Errorf("keep_until = %v, want %v", got.KeepUntil, want)
				}
			}
		})
	}
}

func TestRetentionLabelRemovedLater(t *testing.T) {
	h := newHarness(t)
	sj, hd := h.startJob(102, withStatus(executor.Status{State: executor.Gone}))
	h.adopt()
	h.mustTick()
	_ = h.gh.AddLabels(context.Background(), 102, labels.Keep)
	h.clk.add(25 * time.Hour)
	h.mustTick() // extended
	if !h.job(sj.ID).CleanedAt.IsZero() {
		t.Fatal("cleaned despite ard:keep")
	}
	_ = h.gh.RemoveLabel(context.Background(), 102, labels.Keep)
	h.clk.add(23 * time.Hour)
	h.mustTick()
	if !h.job(sj.ID).CleanedAt.IsZero() {
		t.Error("cleaned before the extended time")
	}
	h.clk.add(2 * time.Hour)
	h.mustTick()
	if h.job(sj.ID).CleanedAt.IsZero() {
		t.Error("not cleaned after the label was removed")
	}
	if _, err := os.Stat(hd.LogPath); err == nil {
		t.Error("log not deleted")
	}
}

func TestRetentionDoneJobKeepsLogs(t *testing.T) {
	h := newHarness(t)
	sj, hd := h.startJob(103)
	if err := h.store.Complete(context.Background(), sj.ID, nil); err != nil {
		t.Fatal(err)
	}
	h.clk.add(24 * time.Hour)
	h.mustTick()
	got := h.job(sj.ID)
	if got.CleanedAt.IsZero() {
		t.Error("done job not cleaned 24 h after it ended")
	}
	if _, err := os.Stat(hd.Worktree); err == nil {
		t.Error("worktree kept")
	}
	if _, err := os.Stat(hd.LogPath); err != nil {
		t.Errorf("a done job's log must stay until Prune: %v", err)
	}
}

func TestRetentionNoHandle(t *testing.T) {
	h := newHarness(t)
	sj, _ := h.startJob(104, noHandle)
	if err := h.store.Fail(context.Background(), sj.ID, "x", h.clk.t.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	h.clk.add(time.Hour)
	h.mustTick()
	if h.job(sj.ID).CleanedAt.IsZero() || len(h.ex.cleanups) != 0 {
		t.Error("a job with no handle only gets marked cleaned")
	}
}

func TestPruneHourly(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 59; i++ {
		h.mustTick()
	}
	if len(h.ex.pruned) != 0 {
		t.Fatalf("pruned early: %v", h.ex.pruned)
	}
	h.mustTick()
	if len(h.ex.pruned) != 1 || h.ex.pruned[0] != 7*24*time.Hour {
		t.Errorf("pruned = %v", h.ex.pruned)
	}
}

// --- running with no record ---

func TestNoRecord(t *testing.T) {
	run := labels.Running(labels.StageWorker, "engineer")
	labelAt := t0.Add(-time.Hour)
	setup := func(h *harness, n int, extra ...string) {
		h.gh.SeedIssue(githubIssue(n, append([]string{labels.Request(labels.StageWorker, "engineer"), run}, extra...)...))
		h.gh.SeedTimeline(n, githubLabeled(run, labelAt))
	}
	t.Run("fails the label and comments once", func(t *testing.T) {
		h := newHarness(t)
		setup(h, 5)
		for i := 0; i < 5; i++ {
			h.mustTick()
		}
		l := h.labels(5)
		if !l[labels.ExecutorFailed] || l[run] || !l[labels.Request(labels.StageWorker, "engineer")] {
			t.Errorf("labels = %v", l)
		}
		cs := h.comments(5)
		if len(cs) != 1 || !strings.HasPrefix(cs[0].Body, "<!-- ard:reaper norecord label="+run+" at=") ||
			!strings.Contains(cs[0].Body, "`:running` with no record in local state") {
			t.Errorf("comments = %+v", cs)
		}
		evs, _ := h.store.Events(context.Background(), state.EventFilter{Kind: state.EventFailure})
		if len(evs) != 1 || !strings.Contains(string(evs[0].Data), `"no_record"`) {
			t.Errorf("events = %+v", evs)
		}
	})
	t.Run("only every fifth tick", func(t *testing.T) {
		h := newHarness(t)
		setup(h, 5)
		for i := 0; i < 4; i++ {
			h.mustTick()
		}
		if h.labels(5)[labels.ExecutorFailed] {
			t.Error("ran before the fifth tick")
		}
	})
	t.Run("replay after a crash does not comment twice", func(t *testing.T) {
		h := newHarness(t)
		setup(h, 5)
		h.gh.FailNext("AddLabels", errBoom) // crash between the comment and the labels
		for i := 0; i < 5; i++ {
			_ = h.tick()
		}
		if h.labels(5)[labels.ExecutorFailed] || len(h.comments(5)) != 1 {
			t.Fatalf("after the crash: labels %v comments %d", h.labels(5), len(h.comments(5)))
		}
		for i := 0; i < 5; i++ {
			h.mustTick()
		}
		if !h.labels(5)[labels.ExecutorFailed] || len(h.comments(5)) != 1 {
			t.Errorf("after the replay: labels %v comments %d", h.labels(5), len(h.comments(5)))
		}
	})
	t.Run("an earlier incident's marker does not suppress", func(t *testing.T) {
		h := newHarness(t)
		setup(h, 5)
		h.gh.SeedComment(5, githubComment("<!-- ard:reaper norecord label="+run+" at=1 -->\nold"))
		for i := 0; i < 5; i++ {
			h.mustTick()
		}
		if len(h.comments(5)) != 2 {
			t.Errorf("comments = %d, want the old one and a new one", len(h.comments(5)))
		}
	})
	t.Run("a job that ended 30s ago is skipped", func(t *testing.T) {
		h := newHarness(t)
		sj, _ := h.startJob(5)
		if err := h.store.Complete(context.Background(), sj.ID, nil); err != nil {
			t.Fatal(err)
		}
		_ = h.gh.AddLabels(context.Background(), 5, run) // still there: close-out removes it last
		h.clk.add(30 * time.Second)
		for i := 0; i < 5; i++ {
			h.mustTick()
		}
		if h.labels(5)[labels.ExecutorFailed] {
			t.Error("raced a close-out")
		}
	})
	t.Run("a running job accounts for the label", func(t *testing.T) {
		h := newHarness(t)
		sj, _ := h.startJob(5)
		h.own(sj)
		for i := 0; i < 5; i++ {
			h.mustTick()
		}
		if h.labels(5)[labels.ExecutorFailed] {
			t.Error("failed a live job")
		}
	})
	t.Run("a job claimed after the snapshot is not failed", func(t *testing.T) {
		h := newHarness(t)
		setup(h, 5)
		h.r.d.Reader = &claimingReader{Reader: h.gh, onList: func() {
			if _, err := h.store.CreateJob(context.Background(), state.NewJob{Stage: "worker", Role: "engineer", Issue: 5, Branch: "worker/engineer-x"}); err != nil {
				t.Error(err)
			}
		}}
		writes := h.writes()
		for i := 0; i < 5; i++ {
			h.mustTick()
		}
		if h.labels(5)[labels.ExecutorFailed] || !h.labels(5)[run] || len(h.comments(5)) != 0 || h.writes() != writes {
			t.Errorf("failed a job that was just claimed: labels %v", h.labels(5))
		}
	})
	t.Run("hold is skipped", func(t *testing.T) {
		h := newHarness(t)
		setup(h, 5, labels.Hold)
		for i := 0; i < 5; i++ {
			h.mustTick()
		}
		if h.labels(5)[labels.ExecutorFailed] || len(h.comments(5)) != 0 {
			t.Error("touched a held issue")
		}
	})
	t.Run("dry-run makes no list calls", func(t *testing.T) {
		h := newHarness(t)
		setup(h, 5)
		spy := &spyReader{Reader: h.gh}
		h.r.d.Reader = spy
		h.r.d.Effects = dryEffects{h.r.d.Effects}
		for i := 0; i < 5; i++ {
			h.mustTick()
		}
		if spy.lists != 0 {
			t.Errorf("ListIssues called %d times in dry-run", spy.lists)
		}
	})
}

func TestFinishSkipsJobThatEndedMeanwhile(t *testing.T) {
	h := newHarness(t)
	sj, _ := h.startJob(111, withStatus(executor.Status{State: executor.Gone}))
	h.adopt()
	// The owner finishes the job after the Reaper listed it.
	h.r.d.Parse = func(ctx context.Context, _ *config.Resolved, s state.Job, _ executor.Handle, _ int) (agent.Result, error) {
		return agent.Result{}, h.store.Complete(ctx, s.ID, nil)
	}
	before, _ := h.store.Attempts(context.Background(), sj.ID)
	h.mustTick()
	after, _ := h.store.Attempts(context.Background(), sj.ID)
	if len(after) != 1 || after[0].EndedAt != before[0].EndedAt || after[0].Cause != "" || after[0].ExitCode != nil {
		t.Errorf("attempt overwritten: %+v", after)
	}
	if got := h.job(sj.ID); got.Status != state.StatusDone || len(h.comments(111)) != 0 || len(h.ex.cleanupsOf(sj.ID)) != 0 {
		t.Errorf("job = %+v comments %d cleanups %v", got, len(h.comments(111)), h.ex.cleanups)
	}
}
