package servecmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/state"
)

// repoDir builds a clone of a local bare remote with a minimal valid config
// committed and pushed to main, and returns the config path in the clone.
func repoDir(t *testing.T) string {
	t.Helper()
	cfg, _ := repoWithRemote(t, "max_pods: 2\n")
	return cfg
}

// repoWithRemote is repoDir with the config content given; it also returns
// the bare remote's path.
func repoWithRemote(t *testing.T, config string) (cfgPath, bare string) {
	t.Helper()
	bare, clone := gittest.NewRemote(t)
	pushConfig(t, clone, config)
	return filepath.Join(clone, ".ard", "config.yaml"), bare
}

// pushConfig commits .ard/config.yaml in clone and pushes main.
func pushConfig(t *testing.T, clone, config string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(clone, ".ard"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, ".ard", "config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "-m", "config")
	gittest.Run(t, clone, "push", "origin", "main")
}

func env(t *testing.T, repo, cfg string, dry bool) *cli.Env {
	t.Helper()
	return &cli.Env{
		Globals: cli.Globals{Repo: repo, ConfigPath: cfg, DryRun: dry},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stdout:  io.Discard, Stderr: io.Discard,
	}
}

// fakeBuilder injects fakes. The remote is the clone's own origin (a local
// bare repo), where production uses the github.com URL.
func fakeBuilder(f *fake.Fake, dry bool) builder {
	return func(ctx context.Context, e *cli.Env, _ github.Repo, _ string) (deps, error) {
		root, err := config.RepoRoot(e.ConfigPath)
		if err != nil {
			return deps{}, err
		}
		remote, err := gitx.New("").RemoteURL(ctx, root, "origin")
		if err != nil {
			return deps{}, err
		}
		var fx effects.Effects
		if dry {
			d, err := effects.NewDryRun(effects.DryRunOptions{Log: e.Log})
			if err != nil {
				return deps{}, err
			}
			fx = d
		} else {
			fx = effects.NewLive(f, nil, e.Log)
		}
		return deps{Reader: f, Effects: fx, RemoteURL: remote}, nil
	}
}

func TestServeRequiresRepo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, repo := range []string{"noslash"} {
		err := serve(context.Background(), env(t, repo, repoDir(t), true), fakeBuilder(fake.New(), true))
		if err == nil {
			t.Errorf("repo %q: want error", repo)
		}
	}
}

func TestServeSecondInstanceExits(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := repoDir(t)
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- serve(ctx, env(t, "o/r", cfg, false), fakeBuilder(f, false)) }()

	// Wait until the first instance has finished startup (it pins the
	// status issue last), so cancelling it is a clean shutdown.
	home, _ := os.UserHomeDir()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if f.Pinned(1) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first serve never finished startup")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	err := serve(ctx2, env(t, "O/R", cfg, true), fakeBuilder(f, true))
	if !errors.Is(err, controller.ErrLeaderHeld) {
		t.Fatalf("second serve err = %v, want ErrLeaderHeld", err)
	}
	var ee *cli.ExitError
	if !errors.As(err, &ee) || ee.Code != 1 {
		t.Errorf("want ExitError code 1, got %v", err)
	}
	lp, _ := controller.LockPath(home, "o", "r")
	if _, statErr := os.Stat(lp); statErr != nil {
		t.Error(statErr)
	}
	cancel()
	if err := <-first; err != nil {
		t.Fatalf("first serve: %v", err)
	}
	// Lock released: a new serve can start and stop.
	buf := &syncBuf{}
	e3 := env(t, "o/r", cfg, true)
	e3.Log = slog.New(slog.NewTextHandler(buf, nil))
	if err := runUntil(t, e3, fakeBuilder(f, true), func() bool { return buf.has("controller.start") }); err != nil {
		t.Fatalf("serve after release: %v", err)
	}
}

// syncBuf is a goroutine-safe log destination.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *syncBuf) has(sub string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Contains(s.b.String(), sub)
}

// runUntil starts serve, waits until cond holds (startup is done), then
// cancels and returns serve's result.
func runUntil(t *testing.T, e *cli.Env, b builder, cond func() bool) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := make(chan error, 1)
	go func() { res <- serve(ctx, e, b) }()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		select {
		case err := <-res:
			t.Fatalf("serve returned before startup finished: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("startup did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	return <-res
}

func TestServeDryRunWritesNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	buf := &syncBuf{}
	e := env(t, "o/r", repoDir(t), true)
	e.Log = slog.New(slog.NewTextHandler(buf, nil))
	if err := runUntil(t, e, fakeBuilder(f, true), func() bool { return buf.has("controller.start") }); err != nil {
		t.Fatal(err)
	}
	if w := f.Writes(); len(w) != 0 {
		t.Errorf("dry-run reached the fake: %v", w)
	}
	home := os.Getenv("HOME")
	dir, err := state.Dir(filepath.Join(home, ".ard"), "o/r", true)
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(context.Background(), dir, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Meta(context.Background(), controller.StatusIssueKey); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("dry-run cached a status issue: %v", err)
	}
	live, _ := state.Dir(filepath.Join(home, ".ard"), "o/r", false)
	if _, err := os.Stat(filepath.Join(live, state.ControllerFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dry-run created the live controller db: %v", err)
	}
}

// lagSearch simulates a search index that has not caught up with creates.
type lagSearch struct{ github.Reader }

func (lagSearch) SearchIssues(context.Context, string, github.SearchOpts) ([]github.Issue, error) {
	return nil, nil
}

func TestServeLiveRestartReusesStatusIssue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	cfg := repoDir(t)
	inner := fakeBuilder(f, false)
	b := func(ctx context.Context, e *cli.Env, r github.Repo, tok string) (deps, error) {
		d, err := inner(ctx, e, r, tok)
		d.Reader = lagSearch{d.Reader}
		return d, err
	}
	if err := runUntil(t, env(t, "o/r", cfg, false), b, func() bool { return f.Pinned(1) }); err != nil {
		t.Fatal(err)
	}
	buf := &syncBuf{}
	e := env(t, "o/r", cfg, false)
	e.Log = slog.New(slog.NewTextHandler(buf, nil))
	if err := runUntil(t, e, b, func() bool { return buf.has("name=status-issue") }); err != nil {
		t.Fatal(err)
	}
	creates := 0
	for _, w := range f.Writes() {
		if w.Method == "CreateIssue" {
			creates++
		}
	}
	if creates != 1 {
		t.Errorf("CreateIssue called %d times, want 1", creates)
	}
	dir, err := state.Dir(filepath.Join(home, ".ard"), "o/r", false)
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(context.Background(), dir, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if v, err := st.Meta(context.Background(), controller.StatusIssueKey); err != nil || v != "1" {
		t.Errorf("cached status issue = %q, %v; want 1", v, err)
	}
}

func TestServeLiveCreatesStatusIssue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	if err := runUntil(t, env(t, "o/r", repoDir(t), false), fakeBuilder(f, false), func() bool { return f.Pinned(1) }); err != nil {
		t.Fatal(err)
	}
	if !f.Pinned(1) {
		t.Errorf("status issue not created and pinned; writes %v", f.Writes())
	}
}

func TestWiringPoolAvailableAfterRunStarts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	var w *wiring
	orig := registerLoops
	registerLoops = func(x *wiring) []controller.Loop {
		w = x
		func() {
			defer func() {
				if recover() == nil {
					t.Error("Pool() in a constructor should panic")
				}
			}()
			x.Pool()
		}()
		return nil
	}
	defer func() { registerLoops = orig }()
	buf := &syncBuf{}
	e := env(t, "o/r", repoDir(t), true)
	e.Log = slog.New(slog.NewTextHandler(buf, nil))
	if err := runUntil(t, e, fakeBuilder(f, true), func() bool { return buf.has("controller.start") }); err != nil {
		t.Fatal(err)
	}
	if w == nil || w.Pool() == nil {
		t.Fatal("pool not adopted")
	}
	if st := w.Pool().Stats(); st.Max != 2 {
		t.Errorf("pool max = %d, want 2 from config", st.Max)
	}
}

func TestRedactText(t *testing.T) {
	secrets := redact.NewSecrets()
	if err := secrets.Add("TOKEN", "hunter2-hunter2"); err != nil {
		t.Fatal(err)
	}
	rd, err := redact.New(secrets, redact.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := redactText(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), rd, "see hunter2-hunter2 here")
	if strings.Contains(got, "hunter2") {
		t.Errorf("secret leaked: %q", got)
	}
}

func TestResolveRepoFromOrigin(t *testing.T) {
	ctx := context.Background()
	_, clone := gittest.NewRemote(t)
	gittest.Run(t, clone, "remote", "set-url", "origin", "git@github.com:Own/Rep.git")
	r, err := resolveRepo(ctx, "", clone)
	if err != nil || r.Owner != "Own" || r.Name != "Rep" {
		t.Fatalf("got %+v, %v", r, err)
	}
	gittest.Run(t, clone, "remote", "set-url", "origin", (&url.URL{Scheme: "https", User: url.UserPassword("user", "secrettoken"), Host: "example.com", Path: "/o/r"}).String())
	if _, err := resolveRepo(ctx, "", clone); err == nil || strings.Contains(err.Error(), "secrettoken") {
		t.Errorf("want error without the URL, got %v", err)
	}
}

func TestRedactTextSurvivesCancelledContextAndLogsFlag(t *testing.T) {
	rd, err := redact.New(redact.NewSecrets(), redact.Options{})
	if err != nil {
		t.Fatal(err)
	}
	buf := &syncBuf{}
	log := slog.New(slog.NewTextHandler(buf, nil))
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	// The closure serve builds detaches from cancellation.
	got := redactText(context.WithoutCancel(parent), log, rd, "plain title")
	if got != "plain title" {
		t.Errorf("got %q", got)
	}
	// A cancelled ctx directly fails closed and says why.
	got = redactText(parent, log, rd, "plain title")
	if !strings.Contains(got, "scan failed") || !buf.has("dryrun.redact_failed") {
		t.Errorf("got %q, log has failed=%v", got, buf.has("dryrun.redact_failed"))
	}
	// A scanner hit logs the count, never the text.
	secret := "AKIAIOSFODNN7EXAMPLE"
	got = redactText(context.Background(), log, rd, "key "+secret)
	if strings.Contains(got, secret) || buf.has(secret) {
		t.Errorf("leak: %q", got)
	}
}

func TestServeRefusesLegacyStateDir(t *testing.T) {
	for _, legacy := range []string{"state/o-r/_controller.db", "state-dryrun/o-r/effects.jsonl"} {
		t.Run(legacy, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			home, _ := os.UserHomeDir()
			p := filepath.Join(home, ".ard", legacy)
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := serve(context.Background(), env(t, "o/r", repoDir(t), true), fakeBuilder(fake.New(), true))
			var ee *cli.ExitError
			if !errors.Is(err, state.ErrLegacyDir) || !errors.As(err, &ee) || ee.Code != 1 {
				t.Fatalf("err = %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(home, ".ard", "locks")); !os.IsNotExist(statErr) {
				t.Errorf("a refused start created the locks dir: %v", statErr)
			}
		})
	}
}

// With --config omitted the repo root defaulted to ".", and a relative RepoDir
// is rejected by executor.Job.Validate, so every scan would fail.
func TestServeRootIsAbsoluteWithoutConfigFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Dir(filepath.Dir(repoDir(t)))
	t.Chdir(dir)
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	var root string
	orig := registerLoops
	registerLoops = func(x *wiring) []controller.Loop { root = x.Root; return nil }
	defer func() { registerLoops = orig }()
	buf := &syncBuf{}
	e := env(t, "o/r", "", true)
	e.Log = slog.New(slog.NewTextHandler(buf, nil))
	if err := runUntil(t, e, fakeBuilder(f, true), func() bool { return buf.has("controller.start") }); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(root) {
		t.Fatalf("wiring.Root = %q, want an absolute path", root)
	}
	home, _ := os.UserHomeDir()
	if want, _ := clonePath(home, github.Repo{Owner: "o", Name: "r"}); root != want {
		t.Fatalf("wiring.Root = %q, want the controller clone %q", root, want)
	}
}

// The working tree's config is ignored (apart from paused): origin/main wins.
func TestServeReadsConfigFromOrigin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := repoDir(t) // origin says max_pods: 2
	if err := os.WriteFile(cfg, []byte("max_pods: [not valid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	var w *wiring
	orig := registerLoops
	registerLoops = func(x *wiring) []controller.Loop { w = x; return nil }
	defer func() { registerLoops = orig }()
	buf := &syncBuf{}
	e := env(t, "o/r", cfg, true)
	e.Log = slog.New(slog.NewTextHandler(buf, nil))
	if err := runUntil(t, e, fakeBuilder(f, true), func() bool { return buf.has("controller.start") }); err != nil {
		t.Fatal(err)
	}
	if st := w.Pool().Stats(); st.Max != 2 {
		t.Errorf("pool max = %d, want 2 from origin/main", st.Max)
	}
	if !buf.has("config_sha=") {
		t.Error("serve.start did not log config_sha")
	}
}

// Unlike every other setting, paused: true in the working tree is honoured.
func TestServeLocalPausedIsHonoured(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := repoDir(t)
	if err := os.WriteFile(cfg, []byte("paused: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	var w *wiring
	orig := registerLoops
	registerLoops = func(x *wiring) []controller.Loop { w = x; return nil }
	defer func() { registerLoops = orig }()
	buf := &syncBuf{}
	e := env(t, "o/r", cfg, true)
	e.Log = slog.New(slog.NewTextHandler(buf, nil))
	if err := runUntil(t, e, fakeBuilder(f, true), func() bool { return buf.has("controller.start") }); err != nil {
		t.Fatal(err)
	}
	if !w.Paused(context.Background()) {
		t.Error("a working-tree paused: true did not pause the post-scan check")
	}
}

// An origin/main that cannot be loaded starts serve paused rather than
// aborting, says why on the status issue, and resumes once it loads.
func TestServeStartsPausedOnInvalidOriginConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, bare := repoWithRemote(t, "max_pods: 0\n")
	clone := filepath.Dir(filepath.Dir(cfg))
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	buf := &syncBuf{}
	e := env(t, "o/r", cfg, false)
	e.Log = slog.New(slog.NewTextHandler(buf, nil))
	var w *wiring
	orig := registerLoops
	registerLoops = func(x *wiring) []controller.Loop { w = x; return nil }
	defer func() { registerLoops = orig }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := make(chan error, 1)
	go func() { res <- serve(ctx, e, fakeBuilder(f, false)) }()
	waitFor(t, "controller.start", func() bool { return buf.has("controller.start") })
	if !buf.has("serve.config_unavailable") || !buf.has("config.initial_failed") {
		t.Errorf("startup did not report the config failure:\n%s", buf.b.String())
	}
	if !f.Pinned(1) {
		t.Error("status issue not created")
	}
	short := strings.TrimSpace(gittest.Run(t, bare, "rev-parse", "main"))[:12]
	waitFor(t, "a status comment naming the SHA", func() bool {
		for _, wr := range f.Writes() {
			if strings.Contains(fmt.Sprint(wr), short) {
				return true
			}
		}
		return false
	})
	// Three more ticks' worth of loads fail the same way: still one comment.
	for range 3 {
		if !w.Paused(ctx) {
			t.Error("Paused = false with an invalid origin config")
		}
	}
	n := 0
	for _, wr := range f.Writes() {
		if strings.Contains(fmt.Sprint(wr), short) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d comments name %s, want exactly 1", n, short)
	}
	// Fixing origin un-pauses: the next load succeeds.
	pushConfig(t, clone, "max_pods: 4\n")
	if w.Paused(ctx) {
		t.Error("still paused after origin/main was fixed")
	}
	cancel()
	if err := <-res; err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestServeCreatesPauseDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home, _ := os.UserHomeDir()
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	buf := &syncBuf{}
	e := env(t, "O/R", repoDir(t), true)
	e.Log = slog.New(slog.NewTextHandler(buf, nil))
	if err := runUntil(t, e, fakeBuilder(f, true), func() bool { return buf.has("controller.start") }); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(home, ".ard", "pause", "o"))
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Errorf("pause dir: %v, %v", fi, err)
	}
	if !buf.has("pause_file=" + filepath.Join(home, ".ard", "pause", "o", "r")) {
		t.Errorf("serve.start does not log the pause file path:\n%s", buf.b.String())
	}
}

func TestServeInvalidRemoteFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := repoDir(t)
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	b := func(ctx context.Context, e *cli.Env, r github.Repo, s string) (deps, error) {
		d, err := fakeBuilder(f, true)(ctx, e, r, s)
		d.RemoteURL = ""
		return d, err
	}
	if err := serve(context.Background(), env(t, "o/r", cfg, true), b); err == nil {
		t.Fatal("want an error for an empty remote URL")
	}
}

func TestServeWarnsHostUntrustedOncePerRole(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, clone := gittest.NewRemote(t)
	if err := os.MkdirAll(filepath.Join(clone, ".ard", "triage", "lead"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, ".ard", "triage", "lead", "prompt.txt"), []byte("route"), 0o644); err != nil {
		t.Fatal(err)
	}
	pushConfig(t, clone, "max_pods: 2\n")
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	buf, errBuf := &syncBuf{}, &syncBuf{}
	e := env(t, "o/r", filepath.Join(clone, ".ard", "config.yaml"), true)
	e.Log = slog.New(slog.NewTextHandler(buf, nil))
	e.Stderr = errBuf
	// A probe loop ticks once the scheduler has reloaded config after the
	// startup load, so the loader has run at least twice.
	probe := &probeLoop{ticked: make(chan struct{})}
	orig := registerLoops
	registerLoops = func(x *wiring) []controller.Loop { return append(orig(x), probe) }
	defer func() { registerLoops = orig }()
	if err := runUntil(t, e, fakeBuilder(f, true), func() bool {
		select {
		case <-probe.ticked:
			return buf.has("controller.start")
		default:
			return false
		}
	}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), "executor.host_untrusted"); n != 1 {
		t.Errorf("host_untrusted logged %d times, want 1:\n%s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "role=lead") {
		t.Errorf("warning does not name the role:\n%s", buf.String())
	}
	if n := strings.Count(errBuf.String(), `role "lead"`); n != 1 {
		t.Errorf("stderr has the warning %d times, want 1:\n%s", n, errBuf.String())
	}
}

// probeLoop closes ticked on its first tick.
type probeLoop struct {
	once   sync.Once
	ticked chan struct{}
}

func (*probeLoop) Name() string                            { return "probe" }
func (*probeLoop) Interval(*config.Resolved) time.Duration { return time.Minute }
func (*probeLoop) Claims() bool                            { return false }
func (p *probeLoop) Tick(context.Context, controller.Env) error {
	p.once.Do(func() { close(p.ticked) })
	return nil
}

func TestRecordHostUntrusted(t *testing.T) {
	tests := []struct {
		name string
		h    executor.Handle
	}{
		{"triage job", executor.Handle{JobID: "J1", Issue: 7, Stage: "triage", Role: "lead"}},
		{"issue-less scanner job", executor.Handle{JobID: "J2", Stage: "scanner", Role: "lint"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, err := state.Open(ctx, t.TempDir(), state.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if err := recordHostUntrusted(st)(ctx, tc.h); err != nil {
				t.Fatal(err)
			}
			evs, err := st.Events(ctx, state.EventFilter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(evs) != 1 {
				t.Fatalf("%d events, want 1: %+v", len(evs), evs)
			}
			e := evs[0]
			if e.Kind != state.EventHostUntrusted || e.JobID != tc.h.JobID || e.Issue != tc.h.Issue || e.Stage != tc.h.Stage || e.Role != tc.h.Role {
				t.Errorf("event = %+v, want handle %+v", e, tc.h)
			}
		})
	}
}

// snapshotGitDir maps every file under dir to its sha256.
func snapshotGitDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		m[p] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// fileRemoteBuilder is fakeBuilder with a file:// remote, which the committer
// accepts (it refuses a bare path), and a non-zero bot ID.
func fileRemoteBuilder(f *fake.Fake) builder {
	inner := fakeBuilder(f, false)
	return func(ctx context.Context, e *cli.Env, r github.Repo, sd string) (deps, error) {
		d, err := inner(ctx, e, r, sd)
		d.RemoteURL = "file://" + d.RemoteURL
		d.Effects = effects.NewLive(f, gitx.New(""), e.Log) // the committer needs real git
		return d, err
	}
}

// The Done-when of #113: serve, the committer and the executor never write to
// the operator's checkout.
func TestServeNeverWritesOperatorCheckout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgPath, bare := repoWithRemote(t, "max_pods: 2\n")
	checkout := filepath.Dir(filepath.Dir(cfgPath))
	gitDir := filepath.Join(checkout, ".git")
	refsBefore := gittest.Run(t, checkout, "for-each-ref")
	wtBefore := gittest.Run(t, checkout, "worktree", "list", "--porcelain")
	before := snapshotGitDir(t, gitDir)

	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", ID: 42, Kind: github.AuthApp}))
	var w *wiring
	orig := registerLoops
	registerLoops = func(x *wiring) []controller.Loop { w = x; return nil }
	defer func() { registerLoops = orig }()
	buf := &syncBuf{}
	e := env(t, "o/r", cfgPath, false)
	e.Log = slog.New(slog.NewTextHandler(buf, nil))
	var job error
	err := runUntil(t, e, fileRemoteBuilder(f), func() bool {
		if !buf.has("controller.start") {
			return false
		}
		if w.cfg.Load() == nil {
			return false
		}
		job = func() error {
			ctx := context.Background()
			if _, err := w.mainCommitter().Commit(ctx, committer.Request{
				Issue: 1, Stage: "scanner", Role: "cve", Summary: "x",
				Files: []committer.File{{Path: ".ard/1/x.md", Data: []byte("x\n")}},
			}); err != nil {
				return fmt.Errorf("commit: %w", err)
			}
			ex, err := w.newExecutor()
			if err != nil {
				return err
			}
			h, err := ex.Prepare(ctx, executor.Job{
				ID: "JOB1", Repo: "o/r", Issue: 1, Stage: "scanner", Role: "cve", RepoDir: w.Root,
				Ref: w.cfg.Load().SHA, RoleDir: t.TempDir(), ReadOnly: true, TrustedInput: true, Timeout: time.Minute,
			})
			if err != nil {
				return fmt.Errorf("prepare: %w", err)
			}
			return ex.Cleanup(ctx, h, executor.CleanupOptions{})
		}()
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if job != nil {
		t.Fatal(job)
	}
	after := snapshotGitDir(t, gitDir)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("operator checkout's .git changed")
		for p, sum := range after {
			if before[p] != sum {
				t.Logf("changed or new: %s", p)
			}
		}
	}
	if got := gittest.Run(t, checkout, "for-each-ref"); got != refsBefore {
		t.Errorf("checkout refs changed:\n%s\nwas:\n%s", got, refsBefore)
	}
	if got := gittest.Run(t, checkout, "worktree", "list", "--porcelain"); got != wtBefore {
		t.Errorf("checkout worktrees changed:\n%s", got)
	}
	home, _ := os.UserHomeDir()
	clone, _ := clonePath(home, github.Repo{Owner: "o", Name: "r"})
	for _, ref := range []string{"refs/ard/config/main", "refs/ard/upstream/main"} {
		gittest.Run(t, clone, "rev-parse", "--verify", ref)
	}
	if got := gittest.Run(t, bare, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(got, ".ard/1/x.md") {
		t.Errorf("remote main lacks the commit:\n%s", got)
	}
}

func TestServeSecondStartReusesClone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := repoDir(t)
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	var logs [2]*syncBuf
	for i := range logs {
		logs[i] = &syncBuf{}
		e := env(t, "o/r", cfg, true)
		e.Log = slog.New(slog.NewTextHandler(logs[i], nil))
		b := logs[i]
		if err := runUntil(t, e, fakeBuilder(f, true), func() bool { return b.has("controller.start") }); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			home, _ := os.UserHomeDir()
			clone, _ := clonePath(home, github.Repo{Owner: "o", Name: "r"})
			gittest.Run(t, clone, "update-ref", "refs/ard/marker", gitOutT(t, clone, "rev-parse", seedRef))
		}
	}
	if !logs[0].has("serve.clone_created") || logs[1].has("serve.clone_created") {
		t.Errorf("clone_created: first=%v second=%v", logs[0].has("serve.clone_created"), logs[1].has("serve.clone_created"))
	}
	home, _ := os.UserHomeDir()
	clone, _ := clonePath(home, github.Repo{Owner: "o", Name: "r"})
	gittest.Run(t, clone, "rev-parse", "--verify", "refs/ard/marker")
}

func gitOutT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(gittest.Run(t, dir, args...))
}

func TestServeRefusesForeignClonePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := repoDir(t)
	p, _ := clonePath(home, github.Repo{Owner: "o", Name: "r"})
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	err := serve(context.Background(), env(t, "o/r", cfg, true), fakeBuilder(f, true))
	if err == nil || !strings.Contains(err.Error(), p) {
		t.Fatalf("err = %v, want one naming %s", err, p)
	}
	if b, _ := os.ReadFile(p); string(b) != "mine" {
		t.Errorf("file changed: %q", b)
	}
	lockPath, _ := controller.LockPath(home, "o", "r")
	l, err := controller.AcquireLeader(lockPath)
	if err != nil {
		t.Fatalf("leader lock still held: %v", err)
	}
	l.Release()
}

func TestServeRefusesOldGit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	shim := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\necho 'git version 2.39.5'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitBin = shim
	t.Cleanup(func() { gitBin = "" })
	err := serve(context.Background(), env(t, "o/r", repoDir(t), true), fakeBuilder(fake.New(), true))
	var ee *cli.ExitError
	if err == nil || !errors.As(err, &ee) || ee.Code != 1 || !strings.Contains(err.Error(), "ard serve:") || !strings.Contains(err.Error(), "2.39.5") {
		t.Fatalf("err = %v", err)
	}
	home, _ := os.UserHomeDir()
	if _, statErr := os.Stat(filepath.Join(home, ".ard")); !os.IsNotExist(statErr) {
		t.Errorf("a refused start created state: %v", statErr)
	}
}

func TestServeAgentImageValidation(t *testing.T) {
	digest := "x/y@sha256:" + strings.Repeat("a", 64)
	orig := dockerProbe
	defer func() { dockerProbe = orig }()

	t.Run("invalid ref", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv(EnvAgentImage, "ard-agent:dev")
		dockerProbe = func(context.Context) ([]byte, error) { t.Error("probe ran for an invalid ref"); return nil, nil }
		f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
		err := serve(context.Background(), env(t, "o/r", repoDir(t), true), fakeBuilder(f, true))
		if !errors.Is(err, executor.ErrImageRef) {
			t.Fatalf("err = %v, want ErrImageRef", err)
		}
	})
	t.Run("docker down", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv(EnvAgentImage, digest)
		dockerProbe = func(context.Context) ([]byte, error) { return []byte("cannot connect"), errors.New("exit status 1") }
		f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
		err := serve(context.Background(), env(t, "o/r", repoDir(t), true), fakeBuilder(f, true))
		if err == nil || !strings.Contains(err.Error(), "Docker is not available") {
			t.Fatalf("err = %v, want Docker is not available", err)
		}
	})
	t.Run("valid ref logs", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv(EnvAgentImage, digest)
		dockerProbe = func(context.Context) ([]byte, error) { return []byte("27.0"), nil }
		f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
		buf := &syncBuf{}
		e := env(t, "o/r", repoDir(t), true)
		e.Log = slog.New(slog.NewTextHandler(buf, nil))
		if err := runUntil(t, e, fakeBuilder(f, true), func() bool { return buf.has("serve.agent_image") && buf.has("controller.start") }); err != nil {
			t.Fatal(err)
		}
		if !buf.has("image=" + digest) {
			t.Errorf("serve.agent_image does not log the ref:\n%s", buf.String())
		}
	})
}
