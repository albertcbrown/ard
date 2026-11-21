package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

type fakeGit struct {
	mu       sync.Mutex
	fetches  int
	fetchErr error
	sha      string
	refspec  string
}

func (g *fakeGit) FetchRef(_ context.Context, _, _, refspec string, _ gitx.TokenFunc) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fetches++
	g.refspec = refspec
	return g.fetchErr
}

func (g *fakeGit) RevParse(context.Context, string, string) (string, error) { return g.sha, nil }

type fakeJobs struct {
	mu        sync.Mutex
	created   []state.NewJob
	completed []string
	failed    []string
	handles   int
	createErr error
}

func (j *fakeJobs) CreateJob(_ context.Context, n state.NewJob) (state.Job, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.createErr != nil {
		return state.Job{}, j.createErr
	}
	j.created = append(j.created, n)
	return state.Job{ID: "01JOB", Stage: n.Stage, Role: n.Role}, nil
}
func (j *fakeJobs) SetHandle(context.Context, string, json.RawMessage) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.handles++
	return nil
}
func (j *fakeJobs) Complete(_ context.Context, id string, _ *float64) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.completed = append(j.completed, id)
	return nil
}
func (j *fakeJobs) Fail(_ context.Context, id, _ string, _ time.Time) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.failed = append(j.failed, id)
	return nil
}

type fakeExecutor struct {
	mu         sync.Mutex
	job        executor.Job
	cmd        executor.Command
	prepared   int
	cleaned    int
	prepareErr error
	statuses   []executor.Status // popped per Status call; the last repeats
	stdout     string
	combined   string
	streams    []executor.LogStream
}

func (e *fakeExecutor) Prepare(_ context.Context, j executor.Job) (executor.Handle, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.prepareErr != nil {
		return executor.Handle{}, e.prepareErr
	}
	e.prepared++
	e.job = j
	return executor.Handle{JobID: j.ID, Stage: j.Stage, Role: j.Role, View: executor.Paths{Work: "/w", Role: "/role"}}, nil
}

func (e *fakeExecutor) Start(_ context.Context, h executor.Handle, c executor.Command) (executor.Handle, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cmd = c
	return h, nil
}

func (e *fakeExecutor) Status(context.Context, executor.Handle) (executor.Status, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.statuses[0]
	if len(e.statuses) > 1 {
		e.statuses = e.statuses[1:]
	}
	return st, nil
}

func (e *fakeExecutor) Logs(_ context.Context, _ executor.Handle, o executor.LogsOptions) (io.ReadCloser, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.streams = append(e.streams, o.Stream)
	if o.Stream == executor.Stdout {
		return io.NopCloser(strings.NewReader(e.stdout)), nil
	}
	return io.NopCloser(strings.NewReader(e.combined)), nil
}

func (e *fakeExecutor) Cleanup(context.Context, executor.Handle, executor.CleanupOptions) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cleaned++
	return nil
}

type engineFixture struct {
	eng  *ExecEngine
	git  *fakeGit
	ex   *fakeExecutor
	jobs *fakeJobs
	pool *pool.Pool
}

const goodLine = `{"fingerprint":"a","title":"t","severity":"high"}` + "\n"

func newEngineFixture(held ...pool.Held) *engineFixture {
	f := &engineFixture{
		git:  &fakeGit{sha: "deadbeef"},
		ex:   &fakeExecutor{statuses: []executor.Status{{State: executor.Running}, {State: executor.Exited}}, stdout: goodLine, combined: "not json at all\n"},
		jobs: &fakeJobs{},
		pool: pool.New(pool.Options{Max: 1, Held: held}),
	}
	f.eng = NewExecEngine(ExecDeps{
		Git: f.git, Pool: func() Offerer { return f.pool }, Executor: f.ex, Jobs: f.jobs,
		Repo: "o/r", Root: "/repo", RepoDir: "/repo", RemoteURL: "https://github.com/o/r.git",
		Poll: time.Millisecond, Now: func() time.Time { return time.Unix(1000, 0) }, Log: quiet,
	})
	return f
}

func (f *engineFixture) assertReleased(t *testing.T) {
	t.Helper()
	if st := f.pool.Stats(); st.Held != 0 {
		t.Errorf("the slot was not released: %+v", st)
	}
}

func TestExecEngineScan(t *testing.T) {
	f := newEngineFixture()
	role := scannerRole("cve")
	role.Dockerfile = ".ard/scanners/cve/Dockerfile"
	got, err := f.eng.Scan(tctx(t), role, time.Unix(500, 0))
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA != "deadbeef" || len(got.Findings) != 1 || got.Findings[0].Fingerprint != "a" {
		t.Errorf("got %+v", got)
	}
	j := f.ex.job
	if j.Ref != "deadbeef" || !j.ReadOnly || j.Stage != "scanner" || j.Issue != 0 || j.Role != "cve" || j.Repo != "o/r" || j.ID != "01JOB" ||
		j.RoleDir != "/repo/.ard/scanners/cve" || j.Dockerfile != "/repo/.ard/scanners/cve/Dockerfile" || j.Timeout != 30*time.Minute || j.RepoDir != "/repo" {
		t.Errorf("job = %+v", j)
	}
	if f.ex.cmd.Path != "/role/scan" || len(f.ex.cmd.Env) != 0 || len(f.ex.cmd.Fixed) != 0 {
		t.Errorf("command = %+v: no env of any kind may reach a scan", f.ex.cmd)
	}
	if f.git.refspec != "+refs/heads/main:refs/ard/scan/main" {
		t.Errorf("refspec = %q", f.git.refspec)
	}
	if len(f.ex.streams) != 1 || f.ex.streams[0] != executor.Stdout {
		t.Errorf("read streams %v: stdout must come from its own file, not the combined log", f.ex.streams)
	}
	if f.ex.cleaned != 1 || len(f.jobs.completed) != 1 || len(f.jobs.failed) != 0 || f.jobs.handles != 1 {
		t.Errorf("cleanup %d, jobs %+v", f.ex.cleaned, f.jobs)
	}
	if len(f.jobs.created) != 1 || f.jobs.created[0].Stage != "scanner" || f.jobs.created[0].Issue != 0 {
		t.Errorf("job record = %+v", f.jobs.created)
	}
	f.assertReleased(t)
}

func TestExecEngineDeniedSlotDropsTheRun(t *testing.T) {
	busy := pool.Held{Candidate: pool.Candidate{Class: pool.ClassWorker, Role: "w", Number: 1, Since: time.Unix(1, 0)}, JobID: "x"}
	f := newEngineFixture(busy)
	_, err := f.eng.Scan(tctx(t), scannerRole("cve"), time.Time{})
	if !errors.Is(err, ErrNoSlot) {
		t.Fatalf("err = %v", err)
	}
	if f.ex.prepared != 0 || f.git.fetches != 0 || len(f.jobs.created) != 0 {
		t.Errorf("a denied run did work: prepared %d, fetches %d, jobs %d", f.ex.prepared, f.git.fetches, len(f.jobs.created))
	}
}

func TestExecEngineFailures(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *engineFixture)
		check func(t *testing.T, err error)
	}{
		{"non-zero exit", func(f *engineFixture) { f.ex.statuses = []executor.Status{{State: executor.Exited, ExitCode: 2}} },
			func(t *testing.T, err error) {
				var se *ScanError
				if !errors.As(err, &se) || se.Exit != 2 || se.TimedOut || se.SHA != "deadbeef" {
					t.Errorf("err = %v", err)
				}
			}},
		{"timeout", func(f *engineFixture) { f.ex.statuses = []executor.Status{{State: executor.TimedOut, ExitCode: -1}} },
			func(t *testing.T, err error) {
				var se *ScanError
				if !errors.As(err, &se) || !se.TimedOut {
					t.Errorf("err = %v", err)
				}
			}},
		{"gone", func(f *engineFixture) {
			f.ex.statuses = []executor.Status{{State: executor.Gone, Reason: "no process"}}
		},
			func(t *testing.T, err error) {
				var se *ScanError
				if !errors.As(err, &se) || se.Exit != -1 {
					t.Errorf("err = %v", err)
				}
			}},
		{"invalid output", func(f *engineFixture) { f.ex.stdout = goodLine + "garbage\n" },
			func(t *testing.T, err error) {
				var se *ScanError
				if !errors.As(err, &se) || len(se.Decode) != 1 || se.Decode[0].Line != 2 {
					t.Errorf("err = %v", err)
				}
			}},
		{"duplicate fingerprints", func(f *engineFixture) { f.ex.stdout = goodLine + goodLine },
			func(t *testing.T, err error) {
				var se *ScanError
				if !errors.As(err, &se) {
					t.Errorf("err = %v", err)
				}
			}},
		{"prepare fails", func(f *engineFixture) { f.ex.prepareErr = errors.New("no docker") },
			func(t *testing.T, err error) {
				var se *ScanError
				if err == nil || errors.As(err, &se) {
					t.Errorf("err = %v: an infrastructure failure is not a scan failure", err)
				}
			}},
		{"fetch fails", func(f *engineFixture) { f.git.fetchErr = errors.New("network") },
			func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "network") {
					t.Errorf("err = %v", err)
				}
			}},
		{"job record fails", func(f *engineFixture) { f.jobs.createErr = state.ErrConflict },
			func(t *testing.T, err error) {
				if !errors.Is(err, state.ErrConflict) {
					t.Errorf("err = %v", err)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newEngineFixture()
			tt.setup(f)
			res, err := f.eng.Scan(tctx(t), scannerRole("cve"), time.Time{})
			if err == nil || len(res.Findings) != 0 {
				t.Fatalf("res = %+v, err = %v", res, err)
			}
			tt.check(t, err)
			f.assertReleased(t)
			if f.ex.prepared > 0 && f.ex.cleaned != 1 {
				t.Errorf("cleanup ran %d times", f.ex.cleaned)
			}
			if len(f.jobs.created) > 0 && len(f.jobs.failed) != 1 {
				t.Errorf("job not marked failed: %+v", f.jobs)
			}
		})
	}
}

func TestExecEngineShutdownCleansUp(t *testing.T) {
	f := newEngineFixture()
	f.ex.statuses = []executor.Status{{State: executor.Running}}
	ctx, cancel := context.WithCancel(tctx(t))
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, err := f.eng.Scan(ctx, scannerRole("cve"), time.Time{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	f.assertReleased(t)
	if f.ex.cleaned != 1 || len(f.jobs.failed) != 1 {
		t.Errorf("cleanup %d, jobs %+v", f.ex.cleaned, f.jobs)
	}
}
