package setupcmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

func TestCommandBadConfigBeforeNetwork(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ard"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".ard", "config.yaml")
	if err := os.WriteFile(cfg, []byte("executor: remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &cli.Env{Globals: cli.Globals{ConfigPath: cfg, Repo: "o/r"}, Log: quiet, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if err := Command(context.Background(), e, cli.SetupOpts{Check: true}); err == nil || !strings.HasPrefix(err.Error(), "setup:") {
		t.Errorf("err = %v", err)
	}
}

func TestCheckExitsTwoOnDrift(t *testing.T) {
	f := fake.New()
	var out bytes.Buffer
	e := &cli.Env{Log: quiet, Stdout: &out, Stderr: &bytes.Buffer{}}
	err := execute(context.Background(), e, deps(f), cli.SetupOpts{Check: true})
	var ee *cli.ExitError
	if !errors.As(err, &ee) || ee.Code != 2 {
		t.Fatalf("err = %v, want ExitError code 2", err)
	}
	if got, _ := f.ListLabels(context.Background()); len(got) != 0 {
		t.Errorf("check wrote labels: %v", got)
	}
	// Applying fixes the drift; a second check is clean.
	if err := execute(context.Background(), e, deps(f), cli.SetupOpts{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := execute(context.Background(), e, deps(f), cli.SetupOpts{Check: true}); err != nil {
		t.Errorf("check after apply: %v", err)
	}
}

func TestExecuteManualStepsError(t *testing.T) {
	d := deps(fake.New())
	d.Permissions = func(context.Context) Row { return Row{Status: StatusManual, Detail: "fix it"} }
	e := &cli.Env{Log: quiet, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	err := execute(context.Background(), e, d, cli.SetupOpts{})
	if err == nil || !strings.Contains(err.Error(), "manual steps needed") {
		t.Errorf("err = %v", err)
	}
}

func TestLocalVsMain(t *testing.T) {
	ctx := context.Background()
	_, clone := gittest.NewRemote(t)
	t.Run("same", func(t *testing.T) {
		if w, err := LocalVsMain(ctx, clone); err != nil || w != "" {
			t.Errorf("w=%q err=%v", w, err)
		}
	})
	t.Run("differing", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Join(clone, ".ard"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(clone, ".ard", "config.yaml"), []byte("x: 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if w, err := LocalVsMain(ctx, clone); err != nil || !strings.Contains(w, "differs from origin/main") {
			t.Errorf("w=%q err=%v", w, err)
		}
	})
	t.Run("no origin/main", func(t *testing.T) {
		dir := t.TempDir()
		gittest.Run(t, dir, "init", "--initial-branch=main")
		if w, err := LocalVsMain(ctx, dir); err != nil || !strings.Contains(w, "origin/main not found") {
			t.Errorf("w=%q err=%v", w, err)
		}
	})
}
