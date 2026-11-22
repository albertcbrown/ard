package reaper

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agent/claude"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/limits"
	"github.com/albertcbrown/ard/internal/state"
)

// fixtureResult parses a claude testdata fixture.
func fixtureResult(t *testing.T, name string, exit int) agent.Result {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "agent", "claude", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	res, err := claude.New(claude.Options{}).Parse(f, exit)
	if err != nil && !errors.Is(err, agent.ErrNoResult) {
		t.Fatal(err)
	}
	return res
}

// fixtureReset is the resetsAt of the synthetic limit fixtures.
var fixtureReset = time.Unix(1795000000, 0).UTC()

func TestUsageLimitClassifier(t *testing.T) {
	reset := time.Date(2026, 11, 22, 15, 45, 0, 0, time.UTC)
	hit := func(sig agent.LimitSignal, scope agent.LimitScope, fam string) *agent.LimitHit {
		return &agent.LimitHit{Scope: scope, Family: fam, ResetsAt: reset, Signal: sig}
	}
	type want struct {
		ok     bool
		detail string
	}
	tests := []struct {
		name string
		res  agent.Result
		auth agent.Auth
		want want
	}{
		{"ok run with a limit seen", agent.Result{OK: true, LimitHit: hit(agent.SignalRateLimitEvent, agent.ScopeSession, "")}, agent.AuthOAuthToken, want{}},
		{"no limit", agent.Result{}, agent.AuthOAuthToken, want{}},
		{"event, oauth", agent.Result{LimitHit: hit(agent.SignalRateLimitEvent, agent.ScopeSession, "")}, agent.AuthOAuthToken, want{true, "session limit"}},
		{"event, api key", agent.Result{LimitHit: hit(agent.SignalRateLimitEvent, agent.ScopeWeekly, "")}, agent.AuthAPIKey, want{true, "weekly limit"}},
		{"message, no credential", agent.Result{LimitHit: hit(agent.SignalLimitMessage, agent.ScopeModel, "opus")}, "", want{true, "opus limit"}},
		{"sonnet", agent.Result{LimitHit: hit(agent.SignalLimitMessage, agent.ScopeModel, "sonnet")}, agent.AuthOAuthToken, want{true, "sonnet limit"}},
		{"unknown scope", agent.Result{LimitHit: hit(agent.SignalRateLimitEvent, agent.ScopeUnknown, "")}, agent.AuthOAuthToken, want{true, "usage limit"}},
		{"model scope with a family we do not know", agent.Result{LimitHit: hit(agent.SignalRateLimitEvent, agent.ScopeModel, "kimi\n")}, agent.AuthOAuthToken, want{true, "usage limit"}},
		{"rate_limit_error, oauth", agent.Result{LimitHit: hit(agent.SignalRateLimitError, agent.ScopeUnknown, "")}, agent.AuthOAuthToken, want{true, "usage limit"}},
		{"rate_limit_error, api key stays transient", agent.Result{LimitHit: hit(agent.SignalRateLimitError, agent.ScopeUnknown, "")}, agent.AuthAPIKey, want{}},
		{"rate_limit_error, no credential", agent.Result{LimitHit: hit(agent.SignalRateLimitError, agent.ScopeUnknown, "")}, "", want{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, ok := UsageLimitClassifier(tc.auth)(tc.res)
			if ok != tc.want.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.want.ok)
			}
			if !ok {
				return
			}
			if c.Kind != closeout.CauseUsageLimit || c.Retry || !c.Transient || c.Detail != tc.want.detail ||
				c.Family != tc.res.LimitHit.Family || !c.ResetsAt.Equal(reset) {
				t.Errorf("cause = %+v", c)
			}
		})
	}
	t.Run("an api key 429 keeps the transient rate_limit cause", func(t *testing.T) {
		res := agent.Result{APIErrorStatus: 429, LimitHit: hit(agent.SignalRateLimitError, agent.ScopeUnknown, "")}
		if _, ok := UsageLimitClassifier(agent.AuthAPIKey)(res); ok {
			t.Fatal("classified")
		}
		if c := CauseOf(state.Job{}, executor.Status{State: executor.Exited}, res, time.Time{}); c.Kind != closeout.CauseRateLimit || !c.Retry {
			t.Errorf("CauseOf = %+v", c)
		}
	})
}

// Every real capture, classified as a subscription run: only the usage-limit
// ones are limit stops.
func TestUsageLimitClassifierOnRealFixtures(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "agent", "claude", "testdata", "real_*.jsonl"))
	for _, f := range files {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			var meta struct {
				ExitCode int `json:"exit_code"`
			}
			b, err := os.ReadFile(strings.TrimSuffix(f, ".jsonl") + ".meta.json")
			if err != nil || json.Unmarshal(b, &meta) != nil {
				t.Fatalf("meta: %v", err)
			}
			res := fixtureResult(t, name, meta.ExitCode)
			_, ok := UsageLimitClassifier(agent.AuthOAuthToken)(res)
			if want := strings.HasPrefix(name, "real_usage_limit_"); ok != want {
				t.Errorf("classified = %v, want %v", ok, want)
			}
		})
	}
}

// bookAt opens a limits book on the harness store and clock, wires it and the
// OAuth classifier into the Finisher, and moves the clock to two hours before
// the fixtures' reset time.
func (h *harness) withLimits(auth agent.Auth) *limits.Book {
	h.t.Helper()
	h.clk.t = fixtureReset.Add(-2 * time.Hour)
	book, err := limits.Open(context.Background(), h.store, h.clk.now, quiet)
	if err != nil {
		h.t.Fatal(err)
	}
	h.limits = book
	h.classify = UsageLimitClassifier(auth)
	h.rebuild()
	return book
}

func TestUsageLimitStopEndToEnd(t *testing.T) {
	h := newHarness(t)
	book := h.withLimits(agent.AuthOAuthToken)
	ctx := context.Background()
	sj, hd := h.startJob(201, withStatus(executor.Status{State: executor.TimedOut, ExitCode: -1}))
	h.results[sj.ID] = fixtureResult(t, "synthetic_limit_rejected_event.jsonl", 1)
	h.adopt()
	h.mustTick()

	got := h.job(sj.ID)
	if got.Status != state.StatusFailed || !got.LimitStop || got.AutoRetry {
		t.Fatalf("job = %+v", got)
	}
	l := h.labels(201)
	if l[labels.ExecutorFailed] || l[labels.Running(labels.StageWorker, "engineer")] || !l[labels.Request(labels.StageWorker, "engineer")] {
		t.Errorf("labels = %v", l)
	}
	cs := h.comments(201)
	if len(cs) != 1 || !strings.Contains(cs[0].Body, "re-runs after") || !strings.Contains(cs[0].Body, "Paused for usage limit until "+fixtureReset.Format("2006-01-02 15:04 UTC")) ||
		strings.Contains(cs[0].Body, "deadline") {
		t.Errorf("comments = %+v", cs)
	}
	if cl := h.ex.cleanupsOf(sj.ID); len(cl) != 1 || !cl[0].Keep {
		t.Errorf("cleanups = %+v: the worktree must be kept", cl)
	}
	if got.CleanedAt.IsZero() == false {
		t.Error("a limited job's worktree was marked cleaned")
	}
	if u := book.Until(""); !u.Equal(fixtureReset) {
		t.Errorf("book = %v, want %v", u, fixtureReset)
	}
	if evs, _ := h.store.Events(ctx, state.EventFilter{Kind: state.EventFailure}); len(evs) != 0 {
		t.Errorf("failure events = %+v", evs)
	}
	// The next claim re-runs it: linked through after_limit, retry_of unchanged.
	next, _ := h.startJob(201, withStatus(executor.Status{State: executor.Exited}))
	if next.AfterLimit != sj.ID || next.RetryOf != "" {
		t.Errorf("next = %+v", next)
	}
	_ = hd
}

// A limit seen on an owned job that the Reaper killed for a quiet heartbeat is
// a limit stop, not heartbeat_quiet.
func TestUsageLimitBeatsKillReason(t *testing.T) {
	h := newHarness(t)
	book := h.withLimits(agent.AuthOAuthToken)
	sj, hd := h.startJob(202)
	h.own(sj)
	h.clk.add(11 * time.Minute) // quiet for longer than 10 minutes
	h.mustTick()
	if h.job(sj.ID).KillReason != string(closeout.CauseQuiet) {
		t.Fatalf("not killed: %+v", h.job(sj.ID))
	}
	res := fixtureResult(t, "synthetic_api_retry_killed.jsonl", -1)
	out, err := h.fin.Finish(context.Background(), sj.ID, hd, executor.Status{State: executor.Gone, ExitCode: -1}, res, closeout.Cause{})
	if err != nil || out != closeout.OutcomeLimited {
		t.Fatalf("Finish = %s, %v", out, err)
	}
	if got := h.job(sj.ID); !strings.HasPrefix(got.FailReason, "usage_limit") {
		t.Errorf("reason = %q", got.FailReason)
	}
	// No reset time in the stream: the first pause is exactly 30 minutes.
	if u := book.Until(""); !u.Equal(h.clk.t.Add(limits.DefaultPause)) {
		t.Errorf("until = %v, want now+30m (%v)", u, h.clk.t.Add(limits.DefaultPause))
	}
}

func TestUsageLimitAPIKeyIsNotALimitStop(t *testing.T) {
	h := newHarness(t)
	book := h.withLimits(agent.AuthAPIKey)
	sj, _ := h.startJob(203, withStatus(executor.Status{State: executor.Exited, ExitCode: 1}))
	h.results[sj.ID] = fixtureResult(t, "synthetic_api_retry_killed.jsonl", 1)
	h.adopt()
	h.mustTick()
	if got := h.job(sj.ID); got.LimitStop || !book.Until("").IsZero() {
		t.Errorf("an API-key run paused the account: %+v until %v", got, book.Until(""))
	}
}

// A limit stop of a plain-failure-free job leaves its automatic retry unused.
func TestUsageLimitKeepsRetryBudget(t *testing.T) {
	h := newHarness(t)
	h.withLimits(agent.AuthOAuthToken)
	first, _ := h.startJob(204, withStatus(executor.Status{State: executor.Exited, ExitCode: 1}))
	h.results[first.ID] = fixtureResult(t, "synthetic_limit_rejected_event.jsonl", 1)
	h.adopt()
	h.mustTick()
	second, _ := h.startJob(204, withStatus(executor.Status{State: executor.Exited, ExitCode: 1}))
	h.results[second.ID] = agent.Result{APIErrorStatus: 429}
	h.adopt()
	h.mustTick()
	if got := h.job(second.ID); !got.AutoRetry || got.AfterLimit != first.ID {
		t.Fatalf("second = %+v: its one automatic retry should be intact", got)
	}
	third, _ := h.startJob(204)
	if third.RetryOf != second.ID {
		t.Errorf("third = %+v", third)
	}
}

type fakeLimits struct {
	err       error
	exhausted []time.Time
	family    []string
	succeeded []string
}

func (f *fakeLimits) Exhaust(_ context.Context, family string, at time.Time) (time.Time, error) {
	if f.err != nil {
		return time.Time{}, f.err
	}
	f.family = append(f.family, family)
	f.exhausted = append(f.exhausted, at)
	return at, nil
}

func (f *fakeLimits) Succeeded(_ context.Context, family string) error {
	f.succeeded = append(f.succeeded, family)
	return nil
}

// An Exhaust error means nothing is posted and no close-out phase is stored,
// so the next tick tries again: no job starts into a limit that was not recorded.
func TestUsageLimitExhaustErrorFailsClosed(t *testing.T) {
	h := newHarness(t)
	fl := &fakeLimits{err: errBoom}
	h.limits = fl
	h.classify = UsageLimitClassifier(agent.AuthOAuthToken)
	h.rebuild()
	sj, _ := h.startJob(205, withStatus(executor.Status{State: executor.Exited, ExitCode: 1}))
	h.results[sj.ID] = fixtureResult(t, "synthetic_limit_rejected_event.jsonl", 1)
	h.adopt()
	if err := h.tick(); err == nil {
		t.Fatal("tick succeeded")
	}
	got := h.job(sj.ID)
	if got.Status != state.StatusRunning || got.CloseoutPhase != "" || len(h.comments(205)) != 0 {
		t.Errorf("job = %+v, comments = %d", got, len(h.comments(205)))
	}
	fl.err = nil
	h.mustTick()
	if got := h.job(sj.ID); !got.LimitStop || len(h.comments(205)) != 1 {
		t.Errorf("after recovery: %+v", got)
	}
}

// A crash after Exhaust and before the close-out finished: ResumeAll records
// the limit again, from the stored record.
func TestResumeAllRerecordsLimit(t *testing.T) {
	h := newHarness(t)
	h.withLimits(agent.AuthOAuthToken)
	sj, hd := h.startJob(206, withStatus(executor.Status{State: executor.Exited, ExitCode: 1}))
	h.cm.err = context.Canceled // the commit is interrupted: the record stays in phase prepared
	res := fixtureResult(t, "synthetic_limit_rejected_event.jsonl", 1)
	if _, err := h.fin.Finish(context.Background(), sj.ID, hd, executor.Status{State: executor.Exited, ExitCode: 1}, res, closeout.Cause{}); err == nil {
		t.Fatal("expected the interrupted close-out")
	}
	if got := h.job(sj.ID); got.CloseoutPhase == "" || got.Status != state.StatusRunning {
		t.Fatalf("job = %+v", got)
	}
	// The restarted process has a book that lost the write.
	fresh := &fakeLimits{}
	h.limits = fresh
	h.cm.err = nil
	h.restart()
	if err := h.r.ResumeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.job(sj.ID); got.Status != state.StatusFailed || !got.LimitStop {
		t.Errorf("job = %+v", got)
	}
	if len(fresh.exhausted) != 1 || !fresh.exhausted[0].Equal(fixtureReset) || fresh.family[0] != "" {
		t.Errorf("re-recorded = %v %q", fresh.exhausted, fresh.family)
	}
	// A pause that already ended is not recorded again.
	fresh.exhausted = nil
	h.clk.t = fixtureReset.Add(time.Minute)
	h.r.rerecordLimit(context.Background(), h.job(sj.ID))
	if len(fresh.exhausted) != 0 {
		t.Errorf("an expired pause was recorded: %v", fresh.exhausted)
	}
}

func TestSuccessResetsBackoff(t *testing.T) {
	h := newHarness(t)
	fl := &fakeLimits{}
	h.limits = fl
	h.rebuild()
	sj, hd := h.startJob(207, withStatus(executor.Status{State: executor.Exited}))
	h.committedDone(sj, hd)
	st := executor.Status{State: executor.Exited}
	if out, err := h.fin.Finish(context.Background(), sj.ID, hd, st, agent.Result{OK: true, Model: "claude-opus-4-1"}, closeout.Cause{}); err != nil || out != closeout.OutcomeDone {
		t.Fatalf("Finish = %s, %v", out, err)
	}
	if len(fl.succeeded) != 1 || fl.succeeded[0] != "opus" {
		t.Errorf("succeeded = %v", fl.succeeded)
	}
	// A failed job does not reset it.
	sj2, hd2 := h.startJob(208, withStatus(executor.Status{State: executor.Exited, ExitCode: 1}))
	if _, err := h.fin.Finish(context.Background(), sj2.ID, hd2, executor.Status{State: executor.Exited, ExitCode: 1}, agent.Result{}, closeout.Cause{}); err != nil {
		t.Fatal(err)
	}
	if len(fl.succeeded) != 1 {
		t.Errorf("succeeded = %v", fl.succeeded)
	}
}
