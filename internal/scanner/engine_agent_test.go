package scanner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agent/claude"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/modelauth"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

const agentText = "PLANTED"

// agentExecutor is fakeExecutor with a real output dir and worktree; write
// plays the agent and fills the output dir when the job starts.
type agentExecutor struct {
	*fakeExecutor
	out, wt string
	write   func(t *testing.T, out string)
	t       *testing.T
}

func (e *agentExecutor) Prepare(ctx context.Context, j executor.Job) (executor.Handle, error) {
	h, err := e.fakeExecutor.Prepare(ctx, j)
	if err != nil {
		return h, err
	}
	h.Worktree, h.OutDir = e.wt, e.out
	h.View = executor.Paths{Work: "/work", Role: "/role", Sidecar: "/sidecar", ConfigDir: "/ard-config", Out: "/out"}
	return h, nil
}

func (e *agentExecutor) Start(ctx context.Context, h executor.Handle, c executor.Command) (executor.Handle, error) {
	if e.write != nil {
		e.write(e.t, e.out)
	}
	return e.fakeExecutor.Start(ctx, h, c)
}

// recJobs is fakeJobs that keeps fail reasons and implements LimitFailer.
type recJobs struct {
	*fakeJobs
	reasons []string
	limited []string
}

func (j *recJobs) Fail(ctx context.Context, id, reason string, keep time.Time) error {
	j.reasons = append(j.reasons, reason)
	return j.fakeJobs.Fail(ctx, id, reason, keep)
}

func (j *recJobs) FailLimited(_ context.Context, id, reason string, _ time.Time) error {
	j.reasons = append(j.reasons, reason)
	j.limited = append(j.limited, id)
	return nil
}

type fakeAdapter struct {
	res  agent.Result
	err  error
	spec agent.Spec
}

func (a *fakeAdapter) Name() string { return "fake" }
func (a *fakeAdapter) Command(s agent.Spec) (agent.Command, error) {
	if err := s.Validate(); err != nil {
		return agent.Command{}, err
	}
	a.spec = s
	return agent.Command{Argv: []string{"/bin/fake"}, PassEnv: []string{"CLAUDE_CODE_OAUTH_TOKEN"}}, nil
}
func (a *fakeAdapter) Parse(io.Reader, int) (agent.Result, error) { return a.res, a.err }

type fakeUsage struct{ steps [][2]string }

func (u *fakeUsage) RecordStepUsage(_ context.Context, job, step string, _ state.StepUsage) (bool, error) {
	u.steps = append(u.steps, [2]string{job, step})
	return true, nil
}

type fakeLimits struct {
	exhausted []string
	succeeded int
	err       error
}

func (l *fakeLimits) Exhaust(_ context.Context, family string, resetsAt time.Time) (time.Time, error) {
	l.exhausted = append(l.exhausted, family)
	return resetsAt, l.err
}
func (l *fakeLimits) Succeeded(context.Context, string) error { l.succeeded++; return nil }

type agentFixture struct {
	eng    *AgentEngine
	deps   AgentDeps
	cfg    *config.Resolved
	role   config.Role
	ex     *agentExecutor
	jobs   *recJobs
	ad     *fakeAdapter
	usage  *fakeUsage
	lim    *fakeLimits
	noLim  bool
	pool   *pool.Pool
	logbuf bytes.Buffer
}

const llmLine = `{"fingerprint":"adr-1:internal/foo.go:Server.Start","title":"t","severity":"high","files":["internal/foo.go"]}` + "\n"

func oauthCred(t *testing.T) modelauth.Credential {
	t.Helper()
	c, err := modelauth.Resolve("", claude.New(claude.Options{}).CredentialEnv, func(k string) string {
		if k == "CLAUDE_CODE_OAUTH_TOKEN" {
			return "ard-oauth-test-value-0123456789abcdef"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func writeFindings(s string) func(t *testing.T, out string) {
	return func(t *testing.T, out string) {
		if err := os.WriteFile(filepath.Join(out, "findings.jsonl"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func newAgentFixture(t *testing.T, held ...pool.Held) *agentFixture {
	t.Helper()
	wt := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wt, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "internal", "foo.go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cost := 0.25
	f := &agentFixture{
		cfg: &config.Resolved{Repo: "/snap/abc", SHA: "abc", MaxBudgetUSD: 5,
			Sidecars: map[string]config.Sidecar{"docs": {Repo: "o/docs", Ref: "main"}}},
		role: config.Role{Kind: config.KindScanner, Name: "arch", Dir: ".ard/scanners/arch", PromptPath: ".ard/scanners/arch/prompt.txt",
			Agent: "fake", Sidecar: "docs", Timeout: 30 * time.Minute, Every: 24 * time.Hour, MaxBudgetUSD: 0.5},
		ex: &agentExecutor{fakeExecutor: &fakeExecutor{statuses: []executor.Status{{State: executor.Running}, {State: executor.Exited}}},
			out: t.TempDir(), wt: wt, write: writeFindings(llmLine), t: t},
		jobs:  &recJobs{fakeJobs: &fakeJobs{}},
		ad:    &fakeAdapter{res: agent.Result{OK: true, Subtype: "success", Model: "claude-opus-5-5", CostUSD: &cost, Usage: &agent.Usage{Input: 1, Output: 2}}},
		usage: &fakeUsage{},
		lim:   &fakeLimits{},
		pool:  pool.New(pool.Options{Max: 1, Held: held}),
	}
	reg, err := agent.NewRegistry(f.ad, claude.New(claude.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	f.deps = AgentDeps{
		ExecDeps: ExecDeps{Pool: func() Offerer { return f.pool }, Executor: f.ex, Jobs: f.jobs, Repo: "o/r", RepoDir: "/repo",
			Poll: time.Millisecond, Now: func() time.Time { return time.Unix(1000, 0) },
			Log: slog.New(slog.NewTextHandler(&f.logbuf, &slog.HandlerOptions{Level: slog.LevelDebug}))},
		Agents: reg, Cred: oauthCred(t), Usage: f.usage,
		Limits: func() LimitRecorder {
			if f.noLim {
				return nil
			}
			return f.lim
		},
	}
	f.eng = NewAgentEngine(f.deps)
	return f
}

func (f *agentFixture) scan(t *testing.T) (ScanResult, error) {
	t.Helper()
	return f.eng.Scan(tctx(t), f.cfg, f.role, time.Unix(500, 0))
}

func TestAgentEngineScan(t *testing.T) {
	f := newAgentFixture(t)
	got, err := f.scan(t)
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA != "abc" || len(got.Findings) != 1 || got.Findings[0].Fingerprint != "adr-1:internal/foo.go:Server.Start" {
		t.Errorf("got %+v", got)
	}
	j := f.ex.job
	if !j.ReadOnly || !j.Output || j.TrustedInput || j.Stage != "scanner" || j.Issue != 0 || j.Ref != "abc" || j.ID != "01JOB" ||
		j.Sidecar.Repo != "o/docs" || j.RoleDir != "/snap/abc/.ard/scanners/arch" || j.Timeout != 30*time.Minute {
		t.Errorf("job = %+v", j)
	}
	s := f.ad.spec
	if s.Stage != agent.StageScanner || s.Issue != 0 || s.OutputFile != "/out/findings.jsonl" || s.PromptFile != "/role/prompt.txt" ||
		len(s.ReadDirs) != 1 || s.ReadDirs[0] != "/sidecar" || s.MaxBudgetUSD != 0.5 || s.Auth != agent.AuthOAuthToken {
		t.Errorf("spec = %+v", s)
	}
	if len(f.ex.cmd.Env) != 1 || !strings.HasPrefix(f.ex.cmd.Env[0], "CLAUDE_CODE_OAUTH_TOKEN=") {
		t.Errorf("the command must carry exactly the model credential")
	}
	if len(f.usage.steps) != 1 || f.usage.steps[0] != [2]string{"01JOB", "01JOB"} {
		t.Errorf("usage = %v", f.usage.steps)
	}
	if f.lim.succeeded != 1 || len(f.jobs.completed) != 1 || len(f.jobs.failed) != 0 || f.ex.cleaned != 1 {
		t.Errorf("limits %+v, jobs %+v, cleaned %d", f.lim, f.jobs.fakeJobs, f.ex.cleaned)
	}
	if !strings.Contains(f.logbuf.String(), "scanner.agent_usage") {
		t.Errorf("no usage log: %s", f.logbuf.String())
	}
	if st := f.pool.Stats(); st.Held != 0 {
		t.Errorf("the slot was not released: %+v", st)
	}
}

func TestAgentEngineEmptyFindings(t *testing.T) {
	f := newAgentFixture(t)
	f.ex.write = writeFindings("")
	got, err := f.scan(t)
	if err != nil || got.SHA != "abc" || len(got.Findings) != 0 || len(f.jobs.completed) != 1 {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestAgentEngineRealAdapter(t *testing.T) {
	f := newAgentFixture(t)
	b, err := os.ReadFile(filepath.Join("..", "agent", "claude", "testdata", "real_success.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	f.ex.stdout = string(b)
	f.role.Agent = "claude"
	got, err := f.scan(t)
	if err != nil || len(got.Findings) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if len(f.usage.steps) != 1 || f.lim.succeeded != 1 {
		t.Errorf("usage %v, limits %+v", f.usage.steps, f.lim)
	}
	if args := strings.Join(f.ex.cmd.Args, " "); !strings.Contains(args, "--add-dir=/out") || !strings.Contains(args, "--max-budget-usd") {
		t.Errorf("args = %s", args)
	}
}

func TestAgentEngineFailures(t *testing.T) {
	limitHit := &agent.LimitHit{Scope: agent.ScopeWeekly, Family: "opus", ResetsAt: time.Unix(9000, 0).UTC(), Signal: agent.SignalRateLimitError}
	tests := []struct {
		name      string
		setup     func(f *agentFixture)
		usage     int // usage records expected
		wantLimit bool
		noScanErr bool // a plain error, not a *ScanError
		runOK     bool // the agent run succeeded; only the findings are bad
	}{
		{name: "missing findings file", runOK: true, setup: func(f *agentFixture) { f.ex.write = nil }, usage: 1},
		{name: "symlinked findings file", runOK: true, usage: 1, setup: func(f *agentFixture) {
			f.ex.write = func(t *testing.T, out string) {
				outside := filepath.Join(t.TempDir(), "x")
				if err := os.WriteFile(outside, []byte(llmLine), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(out, "findings.jsonl")); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{name: "findings file is a directory", runOK: true, usage: 1, setup: func(f *agentFixture) {
			f.ex.write = func(t *testing.T, out string) {
				if err := os.Mkdir(filepath.Join(out, "findings.jsonl"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{name: "oversized findings file", runOK: true, usage: 1, setup: func(f *agentFixture) {
			f.ex.write = func(t *testing.T, out string) {
				if err := os.WriteFile(filepath.Join(out, "findings.jsonl"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Truncate(filepath.Join(out, "findings.jsonl"), MaxStreamBytes+1); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{name: "invalid json", runOK: true, usage: 1, setup: func(f *agentFixture) {
			f.ex.write = writeFindings(llmLine + `{"fingerprint":"` + agentText + `","title":"x","severity":"` + agentText + `"}` + "\n")
		}},
		{name: "bad grammar", runOK: true, usage: 1, setup: func(f *agentFixture) {
			f.ex.write = writeFindings(`{"fingerprint":"` + agentText + `:internal/foo.go","title":"` + agentText + `","severity":"low","files":["internal/foo.go"]}` + "\n")
		}},
		{name: "path missing from the worktree", runOK: true, usage: 1, setup: func(f *agentFixture) {
			f.ex.write = writeFindings(`{"fingerprint":"adr-1:internal/gone.go","title":"t","severity":"low","files":["internal/gone.go"]}` + "\n")
		}},
		{name: "exit 1", usage: 1, setup: func(f *agentFixture) {
			f.ex.statuses = []executor.Status{{State: executor.Exited, ExitCode: 1}}
		}},
		{name: "not ok", usage: 1, setup: func(f *agentFixture) {
			f.ad.res.OK, f.ad.res.Subtype, f.ad.res.Summary = false, agentText+" sub", agentText
		}},
		{name: "timed out without a result", usage: 0, setup: func(f *agentFixture) {
			f.ex.statuses = []executor.Status{{State: executor.TimedOut, ExitCode: -1}}
			f.ad.res, f.ad.err = agent.Result{}, agent.ErrNoResult
		}},
		{name: "gone", usage: 1, setup: func(f *agentFixture) {
			f.ex.statuses = []executor.Status{{State: executor.Gone, Reason: "no process"}}
		}},
		{name: "unparsable output", usage: 0, setup: func(f *agentFixture) { f.ad.res, f.ad.err = agent.Result{}, errors.New(agentText) }},
		{name: "usage limit", usage: 1, wantLimit: true, setup: func(f *agentFixture) {
			f.ad.res.OK, f.ad.res.Subtype, f.ad.res.LimitHit = false, "error_during_execution", limitHit
		}},
		{name: "usage limit with no limits book", usage: 1, noScanErr: true, setup: func(f *agentFixture) {
			f.ad.res.OK, f.ad.res.LimitHit = false, limitHit
			f.noLim = true
		}},
		{name: "usage limit the book cannot record", usage: 1, noScanErr: true, setup: func(f *agentFixture) {
			f.ad.res.OK, f.ad.res.LimitHit = false, limitHit
			f.lim.err = errors.New("disk full")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAgentFixture(t)
			tt.setup(f)
			res, err := f.scan(t)
			if err == nil || len(res.Findings) != 0 {
				t.Fatalf("res = %+v, err = %v", res, err)
			}
			var se *ScanError
			switch {
			case tt.wantLimit:
				if !errors.Is(err, ErrUsageLimit) || len(f.jobs.limited) != 1 || len(f.lim.exhausted) != 1 || f.lim.exhausted[0] != "opus" {
					t.Errorf("err = %v, limited %v, exhausted %v", err, f.jobs.limited, f.lim.exhausted)
				}
			case tt.noScanErr:
				if errors.Is(err, ErrUsageLimit) || errors.As(err, &se) || len(f.jobs.failed) != 1 {
					t.Errorf("err = %v: an unrecorded limit must be a recorded, plain failure", err)
				}
			default:
				if !errors.As(err, &se) || se.SHA != "abc" || len(f.jobs.failed) != 1 {
					t.Errorf("err = %v (%T), jobs %+v", err, err, f.jobs.fakeJobs)
				}
			}
			if len(f.usage.steps) != tt.usage {
				t.Errorf("usage records = %d, want %d", len(f.usage.steps), tt.usage)
			}
			if want := map[bool]int{true: 1}[tt.runOK]; f.lim.succeeded != want {
				t.Errorf("Succeeded calls = %d, want %d", f.lim.succeeded, want)
			}
			for _, s := range append([]string{err.Error(), f.logbuf.String()}, f.jobs.reasons...) {
				if strings.Contains(s, agentText) {
					t.Errorf("agent text leaked: %q", s)
				}
			}
			if f.ex.cleaned != 1 {
				t.Errorf("cleanup ran %d times", f.ex.cleaned)
			}
			if st := f.pool.Stats(); st.Held != 0 {
				t.Errorf("the slot was not released: %+v", st)
			}
		})
	}
}

func TestAgentEngineStopsBeforeSpend(t *testing.T) {
	t.Run("zero credential asks for no slot", func(t *testing.T) {
		f := newAgentFixture(t)
		f.deps.Cred = modelauth.Credential{}
		f.deps.Pool = func() Offerer { t.Fatal("a slot was requested"); return nil }
		eng := NewAgentEngine(f.deps)
		if err := eng.Ready(f.role); !errors.Is(err, ErrNoCredential) {
			t.Errorf("Ready = %v", err)
		}
		if _, err := eng.Scan(tctx(t), f.cfg, f.role, time.Time{}); !errors.Is(err, ErrNoCredential) {
			t.Errorf("Scan = %v", err)
		}
	})
	t.Run("slot denied creates no job record", func(t *testing.T) {
		busy := pool.Held{Candidate: pool.Candidate{Class: pool.ClassWorker, Role: "w", Number: 1, Since: time.Unix(1, 0)}, JobID: "x"}
		f := newAgentFixture(t, busy)
		_, err := f.scan(t)
		if !errors.Is(err, ErrNoSlot) || len(f.jobs.created) != 0 || f.ex.prepared != 0 {
			t.Errorf("err = %v, jobs %+v, prepared %d", err, f.jobs.created, f.ex.prepared)
		}
	})
	t.Run("an executor without an output dir", func(t *testing.T) {
		f := newAgentFixture(t)
		f.ex.out = ""
		_, err := f.scan(t)
		var se *ScanError
		if err == nil || errors.As(err, &se) || !strings.Contains(err.Error(), "no output dir") || f.ex.cmd.Path != "" {
			t.Errorf("err = %v, cmd %+v", err, f.ex.cmd)
		}
	})
}
