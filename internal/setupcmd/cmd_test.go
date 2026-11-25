package setupcmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/cli"
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
