package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	help := []string{"Usage:", "serve", "config", "labels", "rebuild-state", "init", "version"}
	tests := []struct {
		name string
		args []string
		want int
		out  []string
		err  []string
	}{
		{"no args prints help", nil, 0, help, nil},
		{"help", []string{"help"}, 0, help, nil},
		{"--help", []string{"--help"}, 0, help, nil},
		{"version", []string{"version"}, 0, []string{"ard dev"}, nil},
		{"--version", []string{"--version"}, 0, []string{"ard dev"}, nil},
		{"unknown command", []string{"frobnicate"}, 2, nil, []string{`unknown command "frobnicate"`}},
		{"unknown flag", []string{"version", "--frobnicate"}, 2, nil, []string{"unknown flag: --frobnicate"}},
		{"rebuild-state missing scanner", []string{"rebuild-state"}, 2, nil, []string{"accepts 1 arg(s), received 0"}},
		{"rebuild-state extra args", []string{"rebuild-state", "cve", "lint"}, 2, nil, []string{"accepts 1 arg(s), received 2"}},
		{"serve rejects args", []string{"serve", "extra"}, 2, nil, []string{`unknown command "extra" for "ard serve"`}},
		{"bad repo", []string{"--repo", "foo", "version"}, 2, nil, []string{`--repo must be "owner/repo", got "foo"`}},
		{"repo with empty name", []string{"--repo", "o/", "version"}, 2, nil, []string{`--repo must be "owner/repo"`}},
		{"repo with extra path", []string{"--repo", "a/b/c", "version"}, 2, nil, []string{`--repo must be "owner/repo"`}},
		{"bad log level", []string{"--log-level", "chatty", "version"}, 2, nil, []string{`unknown log level "chatty"`}},
		{"config parent prints help", []string{"config"}, 0, []string{"Usage:", "check"}, nil},
		{"labels parent prints help", []string{"labels"}, 0, []string{"Usage:", "sync"}, nil},
		{"serve stub", []string{"serve"}, 1, nil, []string{"ard serve: not implemented yet (tracked in #15)"}},
		{"config check stub", []string{"config", "check"}, 1, nil, []string{"ard config check: not implemented yet (tracked in #11)"}},
		{"labels sync stub", []string{"labels", "sync"}, 1, nil, []string{"ard labels sync: not implemented yet (tracked in #22)"}},
		{"rebuild-state stub", []string{"rebuild-state", "cve"}, 1, nil, []string{"ard rebuild-state: not implemented yet (tracked in #23)"}},
		{"init stub", []string{"init"}, 1, nil, []string{"ard init: not implemented yet (tracked in #27)"}},
		{"setup stub", []string{"setup"}, 1, nil, []string{"ard setup: not implemented yet (tracked in #346)"}},
		{"global flags accepted before subcommand", []string{"--repo", "o/r", "--dry-run", "serve"}, 1, nil, []string{"not implemented yet"}},
		{"global flags accepted after subcommand", []string{"serve", "--repo", "o/r", "--dry-run"}, 1, nil, []string{"not implemented yet"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			if got := Run(tt.args, &stdout, &stderr, Handlers{}, "dev"); got != tt.want {
				t.Errorf("Run(%v) exit = %d, want %d (stderr: %q)", tt.args, got, tt.want, stderr.String())
			}
			for _, want := range tt.out {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("Run(%v) stdout %q missing %q", tt.args, stdout.String(), want)
				}
			}
			for _, want := range tt.err {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("Run(%v) stderr %q missing %q", tt.args, stderr.String(), want)
				}
			}
		})
	}
}

func TestRunHelpListsEveryCommand(t *testing.T) {
	var stdout, stderr strings.Builder
	if got := Run([]string{"help"}, &stdout, &stderr, Handlers{}, "dev"); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
	out := stdout.String()
	for _, cmd := range []string{"serve", "config", "labels", "rebuild-state", "init", "version"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("help output missing %q:\n%s", cmd, out)
		}
	}
	if strings.Contains(out, "completion") {
		t.Errorf("help output should not list the cobra completion command:\n%s", out)
	}
}

func TestRunHandlers(t *testing.T) {
	t.Run("handler receives parsed globals and scanner arg", func(t *testing.T) {
		var got Env
		var scanner string
		h := Handlers{
			RebuildState: func(_ context.Context, e *Env, s string) error {
				got, scanner = *e, s
				return nil
			},
		}
		var stdout, stderr strings.Builder
		args := []string{
			"--repo", "o/r", "--config", "custom.yaml", "--dry-run", "--log-level", "debug",
			"rebuild-state", "cve",
		}
		if code := Run(args, &stdout, &stderr, h, "dev"); code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr.String())
		}
		if scanner != "cve" {
			t.Errorf("scanner = %q, want cve", scanner)
		}
		if got.Repo != "o/r" || got.ConfigPath != "custom.yaml" || !got.DryRun || got.LogLevel != "debug" {
			t.Errorf("globals = %+v", got.Globals)
		}
		if got.Log == nil || got.Stdout == nil || got.Stderr == nil {
			t.Errorf("env not fully wired: %+v", got)
		}
	})

	t.Run("globals parse after the subcommand too", func(t *testing.T) {
		var got Env
		h := Handlers{
			RebuildState: func(_ context.Context, e *Env, _ string) error {
				got = *e
				return nil
			},
		}
		var stdout, stderr strings.Builder
		args := []string{"rebuild-state", "cve", "--repo", "o/r", "--dry-run"}
		if code := Run(args, &stdout, &stderr, h, "dev"); code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr.String())
		}
		if got.Repo != "o/r" || !got.DryRun {
			t.Errorf("globals = %+v", got.Globals)
		}
	})

	t.Run("init passes force", func(t *testing.T) {
		var opts InitOpts
		h := Handlers{
			Init: func(_ context.Context, _ *Env, o InitOpts) error {
				opts = o
				return nil
			},
		}
		var stdout, stderr strings.Builder
		if code := Run([]string{"init", "--force"}, &stdout, &stderr, h, "dev"); code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr.String())
		}
		if !opts.Force {
			t.Error("InitOpts.Force = false, want true")
		}
	})

	t.Run("serve --loops reaches the handler", func(t *testing.T) {
		var got []string
		h := Handlers{Serve: func(_ context.Context, _ *Env, o ServeOpts) error { got = o.Loops; return nil }}
		var stdout, stderr strings.Builder
		if code := Run([]string{"serve", "--loops", "scanners,stale", "--loops", "reaper"}, &stdout, &stderr, h, "dev"); code != 0 {
			t.Fatalf("exit = %d (stderr %q)", code, stderr.String())
		}
		if strings.Join(got, ",") != "scanners,stale,reaper" {
			t.Errorf("Loops = %v", got)
		}
		got = []string{"x"}
		if code := Run([]string{"serve"}, &stdout, &stderr, h, "dev"); code != 0 || len(got) != 0 {
			t.Errorf("no --loops: exit %d, Loops = %v, want empty", code, got)
		}
	})

	t.Run("handler error exits 1 and prints", func(t *testing.T) {
		h := Handlers{
			Serve: func(context.Context, *Env, ServeOpts) error { return errors.New("boom") },
		}
		var stdout, stderr strings.Builder
		if code := Run([]string{"serve"}, &stdout, &stderr, h, "dev"); code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "boom") {
			t.Errorf("stderr %q missing %q", stderr.String(), "boom")
		}
	})

	t.Run("ExitError chooses the code", func(t *testing.T) {
		h := Handlers{
			Serve: func(context.Context, *Env, ServeOpts) error {
				return &ExitError{Code: 7, Err: errors.New("nope")}
			},
		}
		var stdout, stderr strings.Builder
		if code := Run([]string{"serve"}, &stdout, &stderr, h, "dev"); code != 7 {
			t.Errorf("exit = %d, want 7", code)
		}
		if !strings.Contains(stderr.String(), "nope") {
			t.Errorf("stderr %q missing %q", stderr.String(), "nope")
		}
	})

	t.Run("handler can write to env writers", func(t *testing.T) {
		h := Handlers{
			Serve: func(_ context.Context, e *Env, _ ServeOpts) error {
				fmt.Fprint(e.Stdout, "hello stdout")
				return nil
			},
		}
		var stdout, stderr strings.Builder
		if code := Run([]string{"serve"}, &stdout, &stderr, h, "dev"); code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "hello stdout") {
			t.Errorf("stdout %q missing %q", stdout.String(), "hello stdout")
		}
	})

	t.Run("handler receives a context", func(t *testing.T) {
		var hasCtx bool
		h := Handlers{
			Serve: func(ctx context.Context, _ *Env, _ ServeOpts) error {
				hasCtx = ctx != nil
				return nil
			},
		}
		var stdout, stderr strings.Builder
		if code := Run([]string{"serve"}, &stdout, &stderr, h, "dev"); code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr.String())
		}
		if !hasCtx {
			t.Error("handler context is nil")
		}
	})
}
