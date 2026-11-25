package reaper

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/state"
)

func TestDecide(t *testing.T) {
	now := t0.Add(time.Hour)
	h := executor.Handle{JobID: "J", StartedAt: now.Add(-5 * time.Minute), Deadline: now.Add(25 * time.Minute)}
	running := func(last time.Duration) executor.Status {
		return executor.Status{State: executor.Running, Alive: true, LastOutput: now.Add(-last)}
	}
	tests := []struct {
		name      string
		owned     bool
		hasHandle bool
		h         executor.Handle
		st        executor.Status
		want      action
		reason    closeout.CauseKind
	}{
		{"owned, fresh output", true, true, h, running(time.Minute), actSkip, ""},
		{"owned, beat exactly 10m", true, true, h, running(10 * time.Minute), actSkip, ""},
		{"owned, beat 10m+1s", true, true, executorHandleStartedAgo(now, time.Hour), running(10*time.Minute + time.Second), actKillOwned, closeout.CauseQuiet},
		{"owned, no output ever but job just started", true, true, h, executor.Status{State: executor.Running}, actSkip, ""},
		{"owned, no output ever and started 11m ago", true, true, executorHandleStartedAgo(now, 11*time.Minute), executor.Status{State: executor.Running}, actKillOwned, closeout.CauseQuiet},
		{"owned, deadline inside the slack", true, true, withDeadline2(h, now.Add(-70*time.Second)), running(time.Minute), actSkip, ""},
		{"owned, deadline past the slack", true, true, withDeadline2(h, now.Add(-72*time.Second)), running(time.Minute), actKillOwned, closeout.CauseDeadline},
		{"owned, quiet wins over deadline", true, true, executorHandleStartedAgo2(now, time.Hour, now.Add(-time.Hour)), running(11 * time.Minute), actKillOwned, closeout.CauseQuiet},
		{"owned, exited", true, true, h, executor.Status{State: executor.Exited}, actSkip, ""},
		{"owned, gone (killed, owner finishing)", true, true, h, executor.Status{State: executor.Gone}, actSkip, ""},
		{"owned, no handle", true, false, executor.Handle{}, executor.Status{}, actSkip, ""},
		{"owned, prepared", true, true, h, executor.Status{State: executor.Prepared}, actSkip, ""},
		{"unowned, no handle", false, false, executor.Handle{}, executor.Status{}, actFinishUnstarted, ""},
		{"unowned, prepared", false, true, h, executor.Status{State: executor.Prepared}, actFinishUnstarted, ""},
		{"unowned, running (driver released)", false, true, h, running(time.Hour), actKillFinish, ""},
		{"unowned, exited", false, true, h, executor.Status{State: executor.Exited}, actFinishNow, ""},
		{"unowned, timed out", false, true, h, executor.Status{State: executor.TimedOut}, actFinishNow, ""},
		{"unowned, gone", false, true, h, executor.Status{State: executor.Gone}, actFinishNow, ""},
		{"unowned, orphaned", false, true, h, executor.Status{State: executor.Orphaned}, actFinishOrphan, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := decide(tc.owned, tc.hasHandle, tc.h, tc.st, now)
			if d.act != tc.want || d.reason != tc.reason {
				t.Errorf("decide = %+v, want act %d reason %q", d, tc.want, tc.reason)
			}
		})
	}
}

func executorHandleStartedAgo(now time.Time, ago time.Duration) executor.Handle {
	return executorHandleStartedAgo2(now, ago, now.Add(time.Hour))
}

func executorHandleStartedAgo2(now time.Time, ago time.Duration, deadline time.Time) executor.Handle {
	return executor.Handle{JobID: "J", StartedAt: now.Add(-ago), Deadline: deadline}
}

func withDeadline2(h executor.Handle, d time.Time) executor.Handle {
	h.Deadline = d
	return h
}

func TestCauseOf(t *testing.T) {
	start := t0.Add(time.Hour)
	before := state.Job{CreatedAt: t0}
	after := state.Job{CreatedAt: start.Add(time.Minute)}
	rl := agent.Result{APIErrorStatus: 429}
	tests := []struct {
		name      string
		sj        state.Job
		st        executor.Status
		res       agent.Result
		kind      closeout.CauseKind
		transient bool
		retry     bool
	}{
		{"kill reason quiet", with(after, "heartbeat_quiet"), executor.Status{State: executor.Gone, LastOutput: t0}, agent.Result{}, closeout.CauseQuiet, false, false},
		{"kill reason deadline", with(after, "deadline"), executor.Status{State: executor.Gone}, agent.Result{}, closeout.CauseDeadline, false, false},
		{"kill reason restart", with(before, "controller_restart"), executor.Status{State: executor.Gone}, agent.Result{}, closeout.CauseRestart, true, true},
		{"kill reason beats timed out", with(after, "heartbeat_quiet"), executor.Status{State: executor.TimedOut}, agent.Result{}, closeout.CauseQuiet, false, false},
		{"kill reason beats rate limit", with(after, "deadline"), executor.Status{State: executor.Exited}, rl, closeout.CauseDeadline, false, false},
		{"kill reason beats gone-after-restart", with(before, "heartbeat_quiet"), executor.Status{State: executor.Gone}, agent.Result{}, closeout.CauseQuiet, false, false},
		{"timed out", after, executor.Status{State: executor.TimedOut}, agent.Result{}, closeout.CauseDeadline, false, false},
		{"timed out beats rate limit", after, executor.Status{State: executor.TimedOut}, rl, closeout.CauseDeadline, false, false},
		{"gone, created before start", before, executor.Status{State: executor.Gone}, agent.Result{}, closeout.CauseRestart, true, true},
		{"gone, created after start", after, executor.Status{State: executor.Gone}, agent.Result{}, closeout.CauseNoProcess, false, false},
		{"gone, created at start", state.Job{CreatedAt: start}, executor.Status{State: executor.Gone}, agent.Result{}, closeout.CauseNoProcess, false, false},
		{"gone beats rate limit", after, executor.Status{State: executor.Gone}, rl, closeout.CauseNoProcess, false, false},
		{"gone evicted", after, executor.Status{State: executor.Gone, End: executor.EndEvicted}, agent.Result{}, closeout.CauseEvicted, true, true},
		{"gone evicted on a retry is not retried again", state.Job{CreatedAt: start, RetryOf: "PREV"}, executor.Status{State: executor.Gone, End: executor.EndEvicted}, agent.Result{}, closeout.CauseEvicted, true, false},
		{"gone clone failed", after, executor.Status{State: executor.Gone, End: executor.EndCloneFailed}, agent.Result{}, closeout.CauseCloneFail, true, true},
		{"gone prep failed", after, executor.Status{State: executor.Gone, End: executor.EndPrepFailed, Reason: "bad"}, agent.Result{}, closeout.CauseNoProcess, false, false},
		{"gone bad result", after, executor.Status{State: executor.Gone, End: executor.EndBadResult}, agent.Result{}, closeout.CauseNoProcess, false, false},
		{"gone no result", before, executor.Status{State: executor.Gone, End: executor.EndNoResult}, agent.Result{}, closeout.CauseNoProcess, false, false},
		{"rate limit 429", after, executor.Status{State: executor.Exited, ExitCode: 1}, rl, closeout.CauseRateLimit, true, true},
		{"rate limit 529", after, executor.Status{State: executor.Exited, ExitCode: 1}, agent.Result{APIErrorStatus: 529}, closeout.CauseRateLimit, true, true},
		{"rate limit but result ok", after, executor.Status{State: executor.Exited}, agent.Result{OK: true, APIErrorStatus: 429}, "", false, false},
		{"api error 400 is not transient", after, executor.Status{State: executor.Exited, ExitCode: 1}, agent.Result{APIErrorStatus: 400}, "", false, false},
		{"normal exit", after, executor.Status{State: executor.Exited}, agent.Result{OK: true}, "", false, false},
		{"restart cause on a retry is not retried again", state.Job{CreatedAt: t0, RetryOf: "PREV"}, executor.Status{State: executor.Gone}, agent.Result{}, closeout.CauseRestart, true, false},
		{"rate limit on a retry is not retried again", state.Job{CreatedAt: start, RetryOf: "PREV"}, executor.Status{State: executor.Exited}, rl, closeout.CauseRateLimit, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := CauseOf(tc.sj, tc.st, tc.res, start)
			if c.Kind != tc.kind || c.Transient != tc.transient || c.Retry != tc.retry {
				t.Errorf("CauseOf = %+v, want kind %q transient %v retry %v", c, tc.kind, tc.transient, tc.retry)
			}
			if c.Kind != "" && c.Detail == "" {
				t.Error("a cause needs a detail")
			}
		})
	}
	if c := CauseOf(with(after, "heartbeat_quiet"), executor.Status{State: executor.Gone, LastOutput: time.Date(2026, 1, 2, 10, 4, 0, 0, time.UTC)}, agent.Result{}, start); c.Detail != "no output since 10:04 UTC" {
		t.Errorf("quiet detail = %q", c.Detail)
	}
}

func with(sj state.Job, kill string) state.Job { sj.KillReason = kill; return sj }

func TestCloseoutJob(t *testing.T) {
	cost := 1.5
	sj := state.Job{ID: "J", Issue: 7, PR: 9, Stage: "review", Role: "r", Attempt: 2, Branch: "worker/x"}
	h := executor.Handle{Worktree: "/w", Home: "/home", BaseSHA: "b", LogPath: "/l", Scrubbed: []string{".ard/7/review-r.md"}}
	for _, tc := range []struct {
		st   executor.Status
		want int
	}{
		{executor.Status{State: executor.Exited, ExitCode: 3}, 3},
		{executor.Status{State: executor.Gone, ExitCode: 0}, -1},
		{executor.Status{State: executor.Orphaned}, -1},
		{executor.Status{State: executor.Prepared}, -1},
	} {
		j := CloseoutJob(sj, h, tc.st, closeout.Cause{Kind: closeout.CauseQuiet}, agent.Result{CostUSD: &cost})
		if j.ExitCode != tc.want || j.ID != "J" || j.Issue != 7 || j.PR != 9 || j.Stage != "review" || j.Attempt != 2 ||
			j.Branch != "worker/x" || j.Worktree != "/w" || j.Home != "/home" || j.BaseSHA != "b" || j.LogPath != "/l" || j.CostUSD != 1.5 || len(j.Scrubbed) != 1 || j.Scrubbed[0] != ".ard/7/review-r.md" ||
			j.Cause.Kind != closeout.CauseQuiet {
			t.Errorf("%v: job = %+v", tc.st.State, j)
		}
	}
}

// The controller persists the handle as JSON; Scrubbed must survive it.
func TestCloseoutJobScrubbedSurvivesHandleJSON(t *testing.T) {
	in := executor.Handle{Worktree: "/w", Scrubbed: []string{".ard/7/triage-lead.md"}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var h executor.Handle
	if err := json.Unmarshal(b, &h); err != nil {
		t.Fatal(err)
	}
	j := CloseoutJob(state.Job{ID: "J", Issue: 7, Stage: "triage", Role: "lead"}, h, executor.Status{State: executor.Exited}, closeout.Cause{}, agent.Result{})
	if len(j.Scrubbed) != 1 || j.Scrubbed[0] != ".ard/7/triage-lead.md" {
		t.Errorf("Scrubbed = %v", j.Scrubbed)
	}
}
