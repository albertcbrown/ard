package servecmd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/state"
)

func TestStartupHookOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	buf := &syncBuf{}
	e := env(t, "o/r", repoDir(t), true)
	e.Log = slog.New(slog.NewTextHandler(buf, nil))
	if err := runUntil(t, e, fakeBuilder(f, true), func() bool { return buf.has("controller.start") }); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	resume, adopt, status := strings.Index(out, "name=closeout-resume"), strings.Index(out, "name=pool-adopt"), strings.Index(out, "name=status-issue")
	if resume < 0 || adopt < 0 || status < 0 || !(resume < adopt && adopt < status) {
		t.Errorf("hook order: closeout-resume at %d, pool-adopt at %d, status-issue at %d\n%s", resume, adopt, status, out)
	}
}

func newTestWiring(t *testing.T) *wiring {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	home, _ := os.UserHomeDir()
	st, err := state.Open(context.Background(), t.TempDir(), state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w := &wiring{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Repo: github.Repo{Owner: "o", Name: "r"},
		Root: t.TempDir(), Home: home, Store: st, Reader: fake.New(), StateDir: t.TempDir(),
	}
	t.Cleanup(w.closeAll)
	return w
}

// One Closer, one main committer, one executor, one Finisher: the loops, the
// scanners and the Reaper must share them.
func TestSharedParts(t *testing.T) {
	w := newTestWiring(t)
	if w.closer() != w.closer() {
		t.Error("closer is not shared")
	}
	if w.mainCommitter() != w.mainCommitter() || w.branchCommitter() != w.branchCommitter() {
		t.Error("committers are not shared")
	}
	a, err := w.newExecutor()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := w.newExecutor(); a != b {
		t.Error("executor is not shared")
	}
	f1, err := w.finisher()
	if err != nil {
		t.Fatal(err)
	}
	f2, _ := w.finisher()
	_, r, _ := w.reaperParts()
	if f1 != f2 || r == nil || r.Name() != "reaper" || r.Claims() {
		t.Errorf("finisher %p %p reaper %v", f1, f2, r)
	}
}

func TestCfgRoles(t *testing.T) {
	w := newTestWiring(t)
	r := cfgRoles{w}
	if _, ok := r.Role(config.KindTriage, "lead"); ok {
		t.Error("a role with no config loaded")
	}
	w.cfg.Store(&config.Resolved{Roles: []config.Role{{Kind: config.KindTriage, Name: "lead"}}})
	if _, ok := r.Role(config.KindTriage, "lead"); !ok {
		t.Error("lead not found")
	}
	if _, ok := r.Role(config.KindWorker, "lead"); ok {
		t.Error("wrong kind matched")
	}
}

// The close-out committer must refuse a branch it cannot name before it builds
// anything, and must route "" to main (which needs the GitHub identity).
func TestCloseoutCommitterRoutes(t *testing.T) {
	w := newTestWiring(t)
	w.Reader = fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", ID: 1, Kind: github.AuthApp}))
	_, err := closeoutCommitter{w}.Commit(context.Background(), "not-a-worker-branch", committer.Request{})
	if !errors.Is(err, committer.ErrInvalid) {
		t.Errorf("branch commit: %v, want ErrInvalid", err)
	}
	if w.br == nil || w.mc != nil {
		t.Errorf("a branch commit must build only the branch committer: br=%v mc=%v", w.br, w.mc)
	}
}

func TestParseFunc(t *testing.T) {
	w := newTestWiring(t)
	parse := w.parseFunc()
	dir := t.TempDir()
	out := filepath.Join(dir, "out.jsonl")
	if err := os.WriteFile(out, []byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Resolved{Roles: []config.Role{{Kind: config.KindWorker, Name: "engineer", Agent: "claude"}}}
	sj := state.Job{Stage: "worker", Role: "engineer"}
	h := executor.Handle{StdoutPath: out}
	res, err := parse(context.Background(), cfg, sj, h, 0)
	if err != nil || !res.OK {
		t.Errorf("parse = %+v, %v", res, err)
	}
	// A killed job has no result event: the partial result is the answer.
	if err := os.WriteFile(out, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res, err := parse(context.Background(), cfg, sj, h, -1); err != nil || res.OK {
		t.Errorf("no result event: %+v, %v", res, err)
	}
	for name, c := range map[string]struct {
		cfg *config.Resolved
		sj  state.Job
		h   executor.Handle
	}{
		"no config":      {nil, sj, h},
		"unknown role":   {cfg, state.Job{Stage: "worker", Role: "nope"}, h},
		"scanner stage":  {cfg, state.Job{Stage: "scanner", Role: "x"}, h},
		"no stdout path": {cfg, sj, executor.Handle{}},
		"missing file":   {cfg, sj, executor.Handle{StdoutPath: filepath.Join(dir, "gone")}},
	} {
		if _, err := parse(context.Background(), c.cfg, c.sj, c.h, 0); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
