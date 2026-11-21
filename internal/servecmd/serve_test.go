package servecmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/state"
)

// repoDir writes a minimal valid config and returns its path.
func repoDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ard"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ard", "config.yaml"), []byte("max_pods: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, ".ard", "config.yaml")
}

func env(t *testing.T, repo, cfg string, dry bool) *cli.Env {
	t.Helper()
	return &cli.Env{
		Globals: cli.Globals{Repo: repo, ConfigPath: cfg, DryRun: dry},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stdout:  io.Discard, Stderr: io.Discard,
	}
}

func fakeBuilder(f *fake.Fake, dry bool) builder {
	return func(ctx context.Context, e *cli.Env, _ github.Repo, _ string) (deps, error) {
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
		return deps{Reader: f, Effects: fx}, nil
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
	gittest.Run(t, clone, "remote", "set-url", "origin", "https://user:secrettoken@example.com/o/r")
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
