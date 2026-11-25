//go:build unix

package kube

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/kube/api"
	"github.com/albertcbrown/ard/internal/executor/kube/api/fake"
	"github.com/albertcbrown/ard/internal/executor/workspace"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/jobpod"
)

const (
	ns      = "jobs"
	sha40   = "0123456789abcdef0123456789abcdef01234567"
	podIP   = "10.1.2.3"
	envName = "ANTHROPIC_API_KEY"
)

type tokens struct {
	mu      sync.Mutex
	minted  [][]github.Repo
	revoked []string
	err     error
}

func (t *tokens) MintReadToken(_ context.Context, r []github.Repo) (github.ScopedToken, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err != nil {
		return github.ScopedToken{}, t.err
	}
	t.minted = append(t.minted, r)
	return github.ScopedToken{Value: "tok-" + string(rune('a'+len(t.minted)))}, nil
}

func (t *tokens) RevokeToken(_ context.Context, v string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.revoked = append(t.revoked, v)
	return nil
}

func (t *tokens) nRevoked() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.revoked)
}

// gitStub is a workspace.Git whose worktrees are plain directories with a .git file.
type gitStub struct{ pruned int }

func (g *gitStub) RevParse(context.Context, string, string) (string, error) { return sha40, nil }
func (g *gitStub) WorktreeAdd(_ context.Context, _, dir, _ string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /repo/.git/worktrees/x\n"), 0o644)
}
func (g *gitStub) WorktreeRemove(_ context.Context, _, dir string, _ bool) error {
	return os.RemoveAll(dir)
}
func (g *gitStub) WorktreePrune(context.Context, string) error { g.pruned++; return nil }

// rewrite sends every request to the test server, whatever its URL says.
type rewrite struct {
	to   *url.URL
	mu   sync.Mutex
	seen []string
}

func (r *rewrite) RoundTrip(q *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.seen = append(r.seen, q.URL.Host)
	r.mu.Unlock()
	q = q.Clone(q.Context())
	q.URL.Scheme, q.URL.Host = r.to.Scheme, r.to.Host
	return http.DefaultTransport.RoundTrip(q)
}

type env struct {
	t    *testing.T
	fake *fake.Fake
	tok  *tokens
	git  *gitStub
	ws   *workspace.Workspace
	ex   *Executor
	srv  *httptest.Server
	rt   *rewrite

	mu     sync.Mutex
	clock  time.Time
	handle http.HandlerFunc // result server behaviour
}

func (e *env) now() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.clock = e.clock.Add(d)
	e.mu.Unlock()
}

func newEnv(t *testing.T, mut func(*Options)) *env {
	t.Helper()
	e := &env{t: t, fake: fake.New(), tok: &tokens{}, git: &gitStub{}, clock: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		h := e.handle
		e.mu.Unlock()
		if h == nil {
			http.Error(w, "unset", 500)
			return
		}
		h(w, r)
	}))
	t.Cleanup(e.srv.Close)
	u, _ := url.Parse(e.srv.URL)
	e.rt = &rewrite{to: u}
	ws, err := workspace.New(workspace.Options{Root: t.TempDir(), Git: e.git})
	if err != nil {
		t.Fatal(err)
	}
	e.ws = ws
	o := Options{Client: e.fake, Tokens: e.tok, WS: ws, Namespace: ns, Repo: "o/r", Instance: "ard-test",
		AgentImage: "agent@sha256:aa", PodImage: "ard@sha256:bb",
		Policy:       executor.EnvPolicy{AllowEnv: map[string]bool{envName: true}, AllowFixed: map[string]bool{"CLAUDE_CONFIG_DIR": true}},
		ConfigDirEnv: "CLAUDE_CONFIG_DIR",
		HTTP:         &http.Client{Transport: e.rt, Timeout: 5 * time.Second},
		Now:          e.now, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Poll: 5 * time.Millisecond, PendingTimeout: time.Minute}
	if mut != nil {
		mut(&o)
	}
	if e.ex, err = New(o); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // stop watchers before the temp dir goes
		e.ex.mu.Lock()
		var all []*liveJob
		for _, lj := range e.ex.live {
			all = append(all, lj)
		}
		e.ex.mu.Unlock()
		for _, lj := range all {
			if lj.stop != nil {
				lj.stop()
			}
			lj.wg.Wait()
		}
	})
	return e
}

func testJob(id string) executor.Job {
	return executor.Job{ID: id, Repo: "o/r", Issue: 7, Stage: "worker", Role: "dev", RepoDir: "/repo", Ref: "HEAD",
		RoleDir: "/role", Timeout: 20 * time.Minute, RoleSource: executor.RoleSource{SHA: sha40, Path: ".ard/roles/dev"},
		Inputs: []executor.File{{Path: ".ard/7/issue.md", Data: []byte("hi")}}}
}

func testCmd() executor.Command {
	return executor.Command{Path: "claude", Args: []string{"-p"}, Env: []string{envName + "=sk-secret"},
		Fixed: []string{"CLAUDE_CONFIG_DIR=" + jobpod.AgentConfig}}
}

func (e *env) start(id string) executor.Handle {
	e.t.Helper()
	ctx := context.Background()
	h, err := e.ex.Prepare(ctx, testJob(id))
	if err != nil {
		e.t.Fatal(err)
	}
	if h, err = e.ex.Start(ctx, h, testCmd()); err != nil {
		e.t.Fatal(err)
	}
	return h
}

func (e *env) key(h executor.Handle) string {
	s, ok := e.fake.Secret(ns, jobpod.PodName(h.JobID))
	if !ok {
		e.t.Fatal("no secret")
	}
	return string(s.Data[jobpod.ItemResultKey])
}

func (e *env) preDone(h executor.Handle, code int32, msg string) {
	err := e.fake.SetInitContainer(ns, jobpod.PodName(h.JobID), "pre",
		api.ContainerState{Terminated: &api.StateTerminated{ExitCode: code, Message: msg}})
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) agentDone(h executor.Handle, code, sig int32) {
	n := jobpod.PodName(h.JobID)
	e.fake.SetPodIP(ns, n, podIP)
	err := e.fake.SetContainer(ns, n, "agent", api.ContainerState{Terminated: &api.StateTerminated{
		ExitCode: code, Signal: sig, FinishedAt: e.now()}})
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) serve(h http.HandlerFunc) {
	e.mu.Lock()
	e.handle = h
	e.mu.Unlock()
}

// waitTerminal polls Status until the state is terminal.
func (e *env) waitTerminal(h executor.Handle) executor.Status {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := executor.WaitFor(ctx, e.ex, h, 5*time.Millisecond)
	if err != nil {
		e.t.Fatalf("wait: %v (last %+v)", err, st)
	}
	return st
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		io.WriteString(tw, data)
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

func okResult(t *testing.T) []byte {
	return tarGz(t, map[string]string{"work/out.txt": "result", "out/stdout": "agent said hi\n"})
}
