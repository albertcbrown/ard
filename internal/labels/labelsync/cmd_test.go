package labelsync

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/labels"
)

func writeRepo(t *testing.T, cfg string) string {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, data string, mode os.FileMode) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), mode); err != nil {
			t.Fatal(err)
		}
	}
	mk(".ard/config.yaml", cfg, 0o644)
	mk(".ard/scanners/cve/scan", "#!/bin/sh\n", 0o755)
	mk(".ard/triage/lead/prompt.txt", "route", 0o644)
	return root
}

func env(root string, dry bool) (*cli.Env, *bytes.Buffer) {
	var out bytes.Buffer
	return &cli.Env{
		Globals: cli.Globals{ConfigPath: filepath.Join(root, ".ard", "config.yaml"), DryRun: dry},
		Log:     quiet, Stdout: &out, Stderr: &bytes.Buffer{},
	}, &out
}

func TestRunLive(t *testing.T) {
	root := writeRepo(t, "max_pods: 5\n")
	f := fake.New()
	e, out := env(root, false)
	open := func(context.Context, string) (deps, error) {
		return deps{Reader: f, Writer: effects.NewLive(f, nil, quiet)}, nil
	}
	if err := run(context.Background(), e, open); err != nil {
		t.Fatal(err)
	}
	// 9 statics + lead running/done + cve scanner.
	want := "create  ard:executor:failed\n"
	if !strings.Contains(out.String(), want) || !strings.HasSuffix(out.String(), "created 12, updated 0, unchanged 0, stale 0\n") {
		t.Errorf("stdout:\n%s", out.String())
	}
	e, out = env(root, false)
	if err := run(context.Background(), e, open); err != nil {
		t.Fatal(err)
	}
	if out.String() != "created 0, updated 0, unchanged 12, stale 0\n" {
		t.Errorf("second run stdout:\n%s", out.String())
	}
}

func TestRunDryRun(t *testing.T) {
	root := writeRepo(t, "max_pods: 5\n")
	f := fake.New()
	e, out := env(root, true)
	d, err := effects.NewDryRun(effects.DryRunOptions{Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	err = run(context.Background(), e, func(context.Context, string) (deps, error) {
		return deps{Reader: f, Writer: d}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "would create ard:pending\n") ||
		!strings.HasSuffix(out.String(), "dry-run: would create 12, would update 0, unchanged 0, stale 0\n") {
		t.Errorf("stdout:\n%s", out.String())
	}
	if len(f.Writes()) != 0 {
		t.Errorf("dry-run wrote %d", len(f.Writes()))
	}
}

func TestRunConfigErrorSkipsGitHub(t *testing.T) {
	tests := []struct{ name, cfg, wantErr string }{
		{"invalid executor", "executor: remote\n", ""},
		{"intake clash", "intake: {mode: label, label: \"ard:needs-human\"}\n", "intake.label"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeRepo(t, tt.cfg)
			e, _ := env(root, false)
			called := false
			err := run(context.Background(), e, func(context.Context, string) (deps, error) {
				called = true
				return deps{}, nil
			})
			if err == nil || called {
				t.Errorf("err=%v called=%v, want an error and no GitHub client", err, called)
			}
			if err != nil && !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestRunCustomIntake(t *testing.T) {
	root := writeRepo(t, "intake: {mode: label, label: \"ard:intake\"}\n")
	f := fake.New()
	open := func(context.Context, string) (deps, error) {
		return deps{Reader: f, Writer: effects.NewLive(f, nil, quiet)}, nil
	}
	e, out := env(root, false)
	if err := run(context.Background(), e, open); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "create  ard:intake\n") || !strings.HasSuffix(out.String(), "created 13, updated 0, unchanged 0, stale 0\n") {
		t.Errorf("stdout:\n%s", out.String())
	}
	e, out = env(root, false)
	if err := run(context.Background(), e, open); err != nil {
		t.Fatal(err)
	}
	if out.String() != "created 0, updated 0, unchanged 13, stale 0\n" {
		t.Errorf("second run stdout:\n%s", out.String())
	}
	// Switching back to the default leaves the custom label stale, not deleted.
	cfgPath := filepath.Join(root, ".ard", "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("intake: {mode: label}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, out = env(root, false)
	if err := run(context.Background(), e, open); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stale   ard:intake\n") || !strings.HasSuffix(out.String(), "stale 1\n") {
		t.Errorf("third run stdout:\n%s", out.String())
	}
	have, err := f.ListLabels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range have {
		found = found || l.Name == "ard:intake"
	}
	if !found {
		t.Error("ard:intake was deleted")
	}
}

func TestPrint(t *testing.T) {
	rep := Report{
		Plan: Plan{
			Create:    []labels.Label{{Name: "ard:pending"}, {Name: "ard:hold"}},
			Update:    []Update{{Have: github.Label{Name: "ard:review:eng-review", Color: "0e8a16", Description: "d"}, Want: labels.Label{Name: "ard:review:eng-review", Color: "0e8a17", Description: "d"}}},
			Unchanged: make([]string, 45),
			Stale:     []string{"ard:old"},
		},
		Failed: map[string]error{"ard:hold": errors.New("boom")},
	}
	var b bytes.Buffer
	Print(&b, rep, false)
	want := `create  ard:pending
create  ard:hold
update  ard:review:eng-review (color 0e8a16 -> 0e8a17)
stale   ard:old
failed  ard:hold: boom
created 1, updated 1, unchanged 45, stale 1
`
	if b.String() != want {
		t.Errorf("live:\n%s\nwant:\n%s", b.String(), want)
	}
	b.Reset()
	Print(&b, rep, true)
	want = `would create ard:pending
would create ard:hold
would update ard:review:eng-review (color 0e8a16 -> 0e8a17)
stale   ard:old
failed  ard:hold: boom
dry-run: would create 2, would update 1, unchanged 45, stale 1
`
	if b.String() != want {
		t.Errorf("dry-run:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestResolveRepo(t *testing.T) {
	ctx := context.Background()
	_, clone := gittest.NewRemote(t)
	gittest.Run(t, clone, "remote", "set-url", "origin", "https://github.com/acme/widgets.git")

	t.Run("flag wins", func(t *testing.T) {
		e, _ := env(clone, false)
		e.Repo = "o/r"
		got, err := resolveRepo(ctx, e, clone)
		if err != nil || got != (github.Repo{Owner: "o", Name: "r"}) {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("origin fallback", func(t *testing.T) {
		e, _ := env(clone, false)
		got, err := resolveRepo(ctx, e, clone)
		if err != nil || got != (github.Repo{Owner: "acme", Name: "widgets"}) {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("remote with a token is not echoed", func(t *testing.T) {
		_, c := gittest.NewRemote(t)
		gittest.Run(t, c, "remote", "set-url", "origin", "https://x:secret@gitlab.com/a/b.git")
		e, _ := env(c, false)
		_, err := resolveRepo(ctx, e, c)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("err = %v, want an error without the token", err)
		}
	})
	t.Run("no remote", func(t *testing.T) {
		e, _ := env(t.TempDir(), false)
		_, err := resolveRepo(ctx, e, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "--repo owner/repo is required") {
			t.Errorf("err = %v", err)
		}
	})
}
