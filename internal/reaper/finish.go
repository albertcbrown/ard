package reaper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/limits"
	"github.com/albertcbrown/ard/internal/state"
)

// Closer is *closeout.Closer.
type Closer interface {
	Run(ctx context.Context, j closeout.Job) (closeout.Outcome, error)
	ResumeJob(ctx context.Context, id string) (closeout.Outcome, error)
}

// Cleaner is the executor call the Finisher needs.
type Cleaner interface {
	Cleanup(ctx context.Context, h executor.Handle, o executor.CleanupOptions) error
}

// FinishStore is the part of state.Store the Finisher uses.
type FinishStore interface {
	Job(ctx context.Context, id string) (state.Job, error)
	EndAttempt(ctx context.Context, id string, n int, e state.AttemptEnd) error
	MarkCleaned(ctx context.Context, id string) error
	RecordStepUsage(ctx context.Context, jobID, step string, u state.StepUsage) (bool, error)
	JobUsage(ctx context.Context, jobID string) (state.JobUsage, error)
}

// Finisher ends a job whose process has stopped. The triage, worker and
// reviewer loops and the Reaper share one.
type Finisher struct {
	Closer  Closer
	Cleaner Cleaner
	Store   FinishStore
	// Start is when this serve process started: a job created before it
	// belongs to a previous process (see CauseOf).
	Start time.Time
	// Classify is the hook for a cause CauseOf cannot see: serve sets it to
	// UsageLimitClassifier. When it reports ok its cause replaces CauseOf's.
	// nil means none. A usage-limit cause must have Retry false so it does not
	// use up the one automatic retry.
	Classify func(res agent.Result) (closeout.Cause, bool)
	// Limits records a usage-limit stop so the pool starts no job into the
	// limit. nil records nothing.
	Limits LimitRecorder
	// Readings records the usage windows of an agent step (never a test
	// step). nil records nothing. The recorder decides which credentials count.
	Readings ReadingRecorder
	Log      *slog.Logger
}

func (f *Finisher) log() *slog.Logger {
	if f.Log != nil {
		return f.Log
	}
	return slog.Default()
}

// NewCause builds a Cause for sj: a transient kind hands off to one automatic
// retry unless sj is itself a retry.
func NewCause(sj state.Job, kind closeout.CauseKind, detail string, transient bool) closeout.Cause {
	return closeout.Cause{Kind: kind, Detail: detail, Transient: transient, Retry: transient && sj.RetryOf == ""}
}

// CauseOf decides how a job whose process is no longer running ended. The
// first rule that matches wins:
//
//  1. The Reaper recorded a kill reason: that cause (heartbeat_quiet and
//     deadline are not transient; controller_restart is).
//  2. The executor timed the job out: deadline.
//  3. A Gone job with an executor End (Kubernetes): evicted and clone_failed
//     are transient; prep_failed, bad_result and no_result are no_process.
//  4. The process is gone: controller_restart (transient) when the job was
//     created before processStart, otherwise no_process. The latter covers a
//     process of this run that vanished, such as a container killed from
//     outside.
//  5. The agent reported a transient API error: rate_limit.
//  6. Otherwise the zero Cause: a normal close-out from the role file and the
//     exit code.
//
// Retry is set for a transient cause when sj is not itself a retry.
func CauseOf(sj state.Job, st executor.Status, res agent.Result, processStart time.Time) closeout.Cause {
	switch {
	case sj.KillReason != "":
		kind := closeout.CauseKind(sj.KillReason)
		switch kind {
		case closeout.CauseQuiet:
			return NewCause(sj, kind, quietDetail(st), false)
		case closeout.CauseDeadline:
			return NewCause(sj, kind, "ran past its deadline", false)
		case closeout.CauseRestart:
			return NewCause(sj, kind, "orphaned by a controller restart", true)
		}
		return NewCause(sj, kind, "killed by the Reaper", false)
	case st.State == executor.TimedOut:
		return NewCause(sj, closeout.CauseDeadline, "ran past its deadline", false)
	case st.State == executor.Gone && st.End != "":
		switch st.End {
		case executor.EndEvicted:
			return NewCause(sj, closeout.CauseEvicted, "pod evicted or never ran", true)
		case executor.EndCloneFailed:
			return NewCause(sj, closeout.CauseCloneFail, "inputs could not be fetched", true)
		}
		detail := st.Reason
		if detail == "" {
			detail = "job ended with " + string(st.End)
		}
		return NewCause(sj, closeout.CauseNoProcess, detail, false)
	case st.State == executor.Gone:
		if sj.CreatedAt.Before(processStart) {
			return NewCause(sj, closeout.CauseRestart, "process gone after a controller restart", true)
		}
		return NewCause(sj, closeout.CauseNoProcess, "process gone without a result", false)
	case !res.OK && res.Transient():
		return NewCause(sj, closeout.CauseRateLimit, fmt.Sprintf("API status %d", res.APIErrorStatus), true)
	}
	return closeout.Cause{}
}

func quietDetail(st executor.Status) string {
	if st.LastOutput.IsZero() {
		return "no output for 10 minutes"
	}
	return "no output since " + st.LastOutput.UTC().Format("15:04") + " UTC"
}

// CloseoutJob maps a state job and its executor handle to a close-out job.
// ExitCode is -1 when the process is gone, orphaned or never started.
func CloseoutJob(sj state.Job, h executor.Handle, st executor.Status, cause closeout.Cause, res agent.Result) closeout.Job {
	exit := st.ExitCode
	switch st.State {
	case executor.Gone, executor.Orphaned, executor.Prepared, "":
		exit = -1
	}
	j := closeout.Job{ID: sj.ID, Issue: sj.Issue, PR: sj.PR, Stage: sj.Stage, Role: sj.Role, Attempt: sj.Attempt,
		StartedAt: sj.CreatedAt, Branch: sj.Branch, Worktree: h.Worktree, Home: h.Home, BaseSHA: h.BaseSHA, Scrubbed: h.Scrubbed, LogPath: h.LogPath, ExitCode: exit, Cause: cause}
	if res.CostUSD != nil {
		j.CostUSD = *res.CostUSD
	}
	return j
}

// Finish closes out a job whose process has stopped, then tidies up: it ends
// the attempt and calls Cleanup, keeping the worktree unless the outcome is
// done or changes (on removal it marks the job cleaned). It never releases the
// pool slot.
//
// Finish re-reads the job row and computes the cause itself. A caller's
// claim-time state.Job is stale: it has no kill reason. A Reaper kill also
// drops the executor's tracker, so the owner sees Gone; without the re-read a
// quiet-heartbeat kill would look like a transient restart and earn a free
// retry. override is for causes the loop knows and CauseOf cannot (for example
// clone_failed); a zero override means use Classify, then CauseOf.
//
// A Run error is returned as is and the close-out stays resumable. An error
// wrapping closeout.ErrBusy means someone else is closing the job.
func (f *Finisher) Finish(ctx context.Context, jobID string, h executor.Handle, st executor.Status, res agent.Result, override closeout.Cause) (closeout.Outcome, error) {
	return f.FinishWith(ctx, jobID, h, st, res, FinishOptions{Cause: override})
}

// FinishOptions are the extras of FinishWith.
type FinishOptions struct {
	// Cause is Finish's override.
	Cause closeout.Cause
	// Worker is the worker loop's result (PR, reviewers, tests, hold). A worker
	// job that is closed out without it, and whose role file says done, needs a
	// human.
	Worker *closeout.WorkerResult
	// ExtraLogs are further host logs to commit beside the handle's log: the
	// earlier agent steps of a worker job.
	ExtraLogs []string
	// Review is the reviewer loop's context (added lines, head moved). A
	// review closed out without it posts every finding in the review body.
	Review *closeout.ReviewContext
}

// FinishWith is Finish with a worker result, extra logs and a review context. The worker loop
// calls SetBranch and SetPR on the job row first, then FinishWith with the
// handle of its final agent step.
func (f *Finisher) FinishWith(ctx context.Context, jobID string, h executor.Handle, st executor.Status, res agent.Result, o FinishOptions) (closeout.Outcome, error) {
	override := o.Cause
	sj, err := f.Store.Job(ctx, jobID)
	if err != nil {
		return "", fmt.Errorf("finish job %s: %w", jobID, err)
	}
	if sj.Status != state.StatusRunning && (sj.CloseoutPhase == "" || sj.CloseoutPhase == string(closeout.PhaseClosed)) {
		return "", fmt.Errorf("finish job %s: %w", jobID, state.ErrNotRunning)
	}
	// A test step (<id>t<k>) runs agent-written code, so its output is never
	// trusted as the CLI's own: a printed usage-limit line there must not block
	// the account. Only the job's own agent step and its fix steps count.
	if h.JobID != "" && !agentStep(jobID, h.JobID) {
		res.LimitHit = nil
	}
	// Readings come only from the job's own agent steps, for the same reason,
	// and are kept even when the run has no result (a killed step).
	if f.Readings != nil && len(res.Windows) > 0 && agentStep(jobID, h.JobID) {
		f.Readings.Observe(ctx, res.Windows)
	}
	cause := override
	if cause.Kind == "" && f.Classify != nil {
		if c, ok := f.Classify(res); ok {
			cause = c
		}
	}
	if cause.Kind == "" {
		cause = CauseOf(sj, st, res, f.Start)
	}
	// A usage-limit stop is recorded before anything else, so the pool blocks
	// the account no later than the comment says. An error here is returned
	// with nothing posted and the next tick retries: no job may start into a
	// limit the book could not record (fail closed).
	if cause.Kind == closeout.CauseUsageLimit && f.Limits != nil {
		until, err := f.Limits.Exhaust(ctx, cause.Family, cause.ResetsAt)
		if err != nil {
			return "", fmt.Errorf("finish job %s: record usage limit: %w", jobID, err)
		}
		cause.ResetsAt = until
	}
	// Record the final step's usage, then read the job's totals (the sum of
	// every step recorded so far). Close-out never fails over accounting: on an
	// error the final step's own numbers stand in.
	cost := res.CostUSD
	var totals state.JobUsage
	recorded := true
	// Only agent steps are recorded: the job's own id or <id>f<k> (a fix step).
	// A test step (<id>t<k>) runs agent-written code, so its output is never
	// parsed into usage.
	if u, ok := StepUsageOf(res); ok && agentStep(jobID, h.JobID) {
		if _, err := f.Store.RecordStepUsage(ctx, jobID, h.JobID, u); err != nil {
			f.log().Warn("reaper.usage_record_failed", "job", jobID, "step", h.JobID, "error", err)
			recorded = false
		}
	}
	haveTotals := false
	if recorded {
		if t, err := f.Store.JobUsage(ctx, jobID); err != nil {
			f.log().Warn("reaper.usage_record_failed", "job", jobID, "error", err)
		} else {
			totals, haveTotals = t, true
			cost = t.CostUSD
		}
	}
	cj := CloseoutJob(sj, h, st, cause, res)
	cj.CostUSD = 0
	if cost != nil {
		cj.CostUSD = *cost
	}
	if haveTotals {
		cj.Usage = usageOf(totals)
	} else if u, ok := StepUsageOf(res); ok {
		cj.Usage = usageOf(state.JobUsage{Models: u.Models, Tokens: u.Tokens, CostUSD: u.CostUSD, Steps: 1})
	}
	// An agent step that reported no tokens is not counted anywhere: mark it on
	// the issue so the day's token count for the role is known to be low.
	if agentStep(jobID, h.JobID) && cause.Kind != closeout.CauseCloneFail && res.Usage == nil {
		cj.TokensUnreported = true
		f.log().Warn("reaper.tokens_unreported", "job", jobID, "stage", sj.Stage, "role", sj.Role)
	}
	cj.Worker, cj.ExtraLogs, cj.Review = o.Worker, o.ExtraLogs, o.Review
	// End the attempt before close-out: a crash after it leaves a resumable
	// close-out and an ended attempt, never the reverse.
	if sj.Attempt > 0 {
		end := state.AttemptEnd{Cause: string(cause.Kind), Transient: cause.Transient}
		code := cj.ExitCode
		end.ExitCode = &code
		end.CostUSD = cost
		if err := f.Store.EndAttempt(ctx, jobID, sj.Attempt, end); err != nil && !errors.Is(err, state.ErrNotFound) {
			return "", fmt.Errorf("finish job %s: end attempt: %w", jobID, err)
		}
	}
	out, err := f.Closer.Run(ctx, cj)
	if err != nil {
		return "", fmt.Errorf("finish job %s: %w", jobID, err)
	}
	attrs := []any{"job", jobID, "issue", sj.Issue, "stage", sj.Stage, "role", sj.Role,
		"cause", string(cause.Kind), "outcome", string(out), "retry", cause.Retry}
	if cause.Kind == closeout.CauseUsageLimit {
		attrs = append(attrs, "family", cause.Family, "until", cause.ResetsAt.UTC())
	}
	f.log().Info("reaper.finish", append(attrs, usageAttrs(cj.Usage)...)...)
	if res.OK && cause.Kind == "" && (out == closeout.OutcomeDone || out == closeout.OutcomeChanges) && f.Limits != nil {
		// A job that ended well proves the account is usable: the no-reset
		// backoff starts over. Best effort.
		if err := f.Limits.Succeeded(ctx, limits.FamilyOf(res.Model)); err != nil {
			f.log().Warn("reaper.limits_succeeded", "job", jobID, "error", err)
		}
	}
	f.Tidy(ctx, sj, h, out)
	return out, nil
}

// usageAttrs are the job's totals for the reaper.finish event: tokens, cost
// and the number of agent steps summed.
func usageAttrs(u closeout.Usage) []any {
	var a []any
	if t := u.Tokens; t != nil {
		a = append(a, "tokens_in", t.Input, "tokens_out", t.Output, "tokens_cache_write", t.CacheWrite, "tokens_cache_read", t.CacheRead)
	}
	if u.CostUSD != nil {
		a = append(a, "cost_usd", *u.CostUSD)
	}
	if u.Steps > 0 {
		a = append(a, "steps", u.Steps)
	}
	return a
}

// usageOf maps a job's state totals to the close-out Usage.
func usageOf(u state.JobUsage) closeout.Usage {
	out := closeout.Usage{Models: u.Models, CostUSD: u.CostUSD, Steps: u.Steps}
	if t := u.Tokens; t != nil {
		out.Tokens = &closeout.Tokens{Input: t.Input, Output: t.Output, CacheWrite: t.CacheWrite, CacheRead: t.CacheRead}
	}
	return out
}

// agentStep reports whether step is an agent or fix step of the job.
func agentStep(jobID, step string) bool {
	if step == jobID {
		return true
	}
	rest, ok := strings.CutPrefix(step, jobID+"f")
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// StepUsageOf maps what one agent run reported to a state record. ok is false
// when the result reported nothing: no cost, no usage and no model. Models are
// the keys of res.ModelUsage plus res.Model, de-duplicated, sorted and limited
// to valid model ids.
func StepUsageOf(res agent.Result) (state.StepUsage, bool) {
	var u state.StepUsage
	if res.CostUSD != nil {
		if c := *res.CostUSD; !math.IsNaN(c) && !math.IsInf(c, 0) && c >= 0 {
			u.CostUSD = &c
		}
	}
	if res.Usage != nil {
		u.Tokens = &state.Tokens{Input: res.Usage.Input, Output: res.Usage.Output, CacheWrite: res.Usage.CacheWrite, CacheRead: res.Usage.CacheRead}
	}
	seen := map[string]bool{}
	add := func(m string) {
		if m != "" && !seen[m] && state.ValidModel(m) {
			seen[m] = true
			u.Models = append(u.Models, m)
		}
	}
	for m := range res.ModelUsage {
		add(m)
	}
	add(res.Model)
	sort.Strings(u.Models)
	if u.CostUSD == nil && u.Tokens == nil && len(u.Models) == 0 {
		return state.StepUsage{}, false
	}
	return u, true
}

// Tidy removes the worktree of a closed-out job unless the outcome keeps it
// (failed, retry, usage-limit, needs-human), and marks the job cleaned when it did. It logs
// and ignores errors: close-out is already done and retention retries later.
func (f *Finisher) Tidy(ctx context.Context, sj state.Job, h executor.Handle, out closeout.Outcome) {
	switch out {
	case closeout.OutcomeFailed, closeout.OutcomeRetry, closeout.OutcomeLimited, closeout.OutcomeNeedsHuman, closeout.OutcomeNeedsNetwork:
		if h.JobID != "" && f.Cleaner != nil {
			if err := f.Cleaner.Cleanup(ctx, h, executor.CleanupOptions{KeepWorktree: true}); err != nil {
				f.log().Warn("reaper.cleanup", "job", sj.ID, "error", err)
			}
		}
		return
	}
	if h.JobID != "" && f.Cleaner != nil {
		if err := f.Cleaner.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
			f.log().Warn("reaper.cleanup", "job", sj.ID, "error", err)
			return
		}
	}
	if err := f.Store.MarkCleaned(ctx, sj.ID); err != nil {
		f.log().Warn("reaper.mark_cleaned", "job", sj.ID, "error", err)
	}
}

// decodeHandle parses a stored executor handle. A missing or malformed handle
// counts as no handle.
func decodeHandle(raw json.RawMessage) (executor.Handle, bool) {
	if len(raw) == 0 {
		return executor.Handle{}, false
	}
	var h executor.Handle
	if err := json.Unmarshal(raw, &h); err != nil || h.JobID == "" {
		return executor.Handle{}, false
	}
	return h, true
}
