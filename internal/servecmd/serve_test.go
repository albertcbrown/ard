package servecmd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
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
	for _, repo := range []string{"", "noslash"} {
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
	if _, statErr := os.Stat(controller.LockPath(home, "o", "r")); statErr != nil {
		t.Error(statErr)
	}
	cancel()
	if err := <-first; err != nil {
		t.Fatalf("first serve: %v", err)
	}
	// Lock released: a new serve can start and stop.
	ctx3, cancel3 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel3()
	if err := serve(ctx3, env(t, "o/r", cfg, true), fakeBuilder(f, true)); err != nil {
		t.Fatalf("serve after release: %v", err)
	}
}

func TestServeDryRunWritesNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := serve(ctx, env(t, "o/r", repoDir(t), true), fakeBuilder(f, true)); err != nil {
		t.Fatal(err)
	}
	if w := f.Writes(); len(w) != 0 {
		t.Errorf("dry-run reached the fake: %v", w)
	}
}

func TestServeLiveCreatesStatusIssue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthApp}))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := serve(ctx, env(t, "o/r", repoDir(t), false), fakeBuilder(f, false)); err != nil {
		t.Fatal(err)
	}
	if !f.Pinned(1) {
		t.Errorf("status issue not created and pinned; writes %v", f.Writes())
	}
}
