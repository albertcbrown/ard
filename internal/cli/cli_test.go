package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	help := []string{"Usage:", "serve", "config", "labels", "rebuild-state", "init", "setup", "version"}
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

	t.Run("init passes the version", func(t *testing.T) {
		var opts InitOpts
		h := Handlers{
			Init: func(_ context.Context, _ *Env, o InitOpts) error {
				opts = o
				return nil
			},
		}
		var stdout, stderr strings.Builder
		if code := Run([]string{"init"}, &stdout, &stderr, h, "dev"); code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %q)", code, stderr.String())
		}
		if opts.Version != "dev" {
			t.Errorf("InitOpts.Version = %q, want dev", opts.Version)
		}
	})

	t.Run("init --pr is parsed", func(t *testing.T) {
		var opts InitOpts
		h := Handlers{Init: func(_ context.Context, _ *Env, o InitOpts) error { opts = o; return nil }}
		var stdout, stderr strings.Builder
		if code := Run([]string{"init", "--pr"}, &stdout, &stderr, h, "dev"); code != 0 {
			t.Fatalf("exit = %d (stderr: %q)", code, stderr.String())
		}
		if !opts.PR {
			t.Error("InitOpts.PR = false")
		}
	})

	t.Run("setup --check and labels sync reach their handlers", func(t *testing.T) {
		var setup []SetupOpts
		synced := false
		h := Handlers{
			Setup:      func(_ context.Context, _ *Env, o SetupOpts) error { setup = append(setup, o); return nil },
			LabelsSync: func(context.Context, *Env) error { synced = true; return nil },
		}
		var stdout, stderr strings.Builder
		if code := Run([]string{"setup", "--check"}, &stdout, &stderr, h, "dev"); code != 0 {
			t.Fatalf("setup --check exit = %d (stderr %q)", code, stderr.String())
		}
		if code := Run([]string{"setup"}, &stdout, &stderr, h, "dev"); code != 0 {
			t.Fatalf("setup exit = %d", code)
		}
		if len(setup) != 2 || !setup[0].Check || setup[1].Check {
			t.Errorf("SetupOpts = %+v", setup)
		}
		if code := Run([]string{"labels", "sync"}, &stdout, &stderr, h, "dev"); code != 0 || !synced {
			t.Errorf("labels sync: exit %d, synced %v", code, synced)
		}
	})

	t.Run("setup --create-gh-app flags", func(t *testing.T) {
		var got SetupOpts
		h := Handlers{Setup: func(_ context.Context, _ *Env, o SetupOpts) error { got = o; return nil }}
		var stdout, stderr strings.Builder
		args := []string{"setup", "--create-gh-app", "--app-name", "n", "--key-file", "/k.pem", "--force"}
		if code := Run(args, &stdout, &stderr, h, "dev"); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr.String())
		}
		if want := (SetupOpts{CreateGHApp: true, AppName: "n", KeyFile: "/k.pem", Force: true}); got != want {
			t.Errorf("got %+v", got)
		}
		if code := Run([]string{"setup", "--create-gh-app", "--check"}, &stdout, &stderr, h, "dev"); code == 0 {
			t.Error("--create-gh-app --check accepted")
		}
		if code := Run([]string{"setup", "--force"}, &stdout, &stderr, h, "dev"); code == 0 {
			t.Error("--force without --create-gh-app accepted")
		}
	})

	t.Run("setup --print-workflow", func(t *testing.T) {
		var got SetupOpts
		h := Handlers{Setup: func(_ context.Context, _ *Env, o SetupOpts) error { got = o; return nil }}
		var stdout, stderr strings.Builder
		if code := Run([]string{"setup", "--print-workflow"}, &stdout, &stderr, h, "dev"); code != 0 || !got.PrintWorkflow {
			t.Fatalf("exit %d, opts %+v", code, got)
		}
		for _, extra := range []string{"--check", "--create-gh-app"} {
			if code := Run([]string{"setup", "--print-workflow", extra}, &stdout, &stderr, h, "dev"); code == 0 {
				t.Errorf("--print-workflow %s accepted", extra)
			}
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

	t.Run("serve --ui and --no-ui", func(t *testing.T) {
		var got string
		h := Handlers{Serve: func(_ context.Context, _ *Env, o ServeOpts) error { got = o.UI; return nil }}
		var stdout, stderr strings.Builder
		for args, want := range map[string]string{"serve": "", "serve --ui": "on", "serve --no-ui": "off"} {
			got = "x"
			if code := Run(strings.Fields(args), &stdout, &stderr, h, "dev"); code != 0 || got != want {
				t.Errorf("%q: exit %d, UI = %q, want %q", args, code, got, want)
			}
		}
		if code := Run([]string{"serve", "--ui", "--no-ui"}, &stdout, &stderr, h, "dev"); code != 2 {
			t.Errorf("--ui --no-ui: exit %d, want 2", code)
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

func TestJobCommands(t *testing.T) {
	const dl = "2030-01-02T03:04:05Z"
	var gotExec JobExecOpts
	var gotPre JobPreOpts
	h := Handlers{
		JobExec: func(_ context.Context, _ *Env, o JobExecOpts) error { gotExec = o; return nil },
		JobPre:  func(_ context.Context, _ *Env, o JobPreOpts) error { gotPre = o; return nil },
	}
	run := func(args ...string) (int, string, string) {
		var so, se strings.Builder
		c := Run(args, &so, &se, h, "dev")
		return c, so.String(), se.String()
	}
	if c, _, se := run("job-exec", "--deadline", dl, "--grace", "3s", "--", "agent", "-x", "--y"); c != 0 {
		t.Fatalf("exit %d: %s", c, se)
	}
	if !gotExec.Deadline.Equal(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)) || gotExec.Grace != 3*time.Second ||
		strings.Join(gotExec.Argv, " ") != "agent -x --y" {
		t.Errorf("exec opts = %+v", gotExec)
	}
	if c, _, _ := run("job-exec", "--deadline", dl); c != 2 {
		t.Errorf("no argv: exit %d, want 2", c)
	}
	if c, _, _ := run("job-exec", "--deadline", dl, "agent"); c != 2 {
		t.Errorf("argv without --: exit %d, want 2", c)
	}
	if c, _, se := run("job-pre", "--deadline", "tomorrow"); c != 2 || !strings.Contains(se, "deadline") {
		t.Errorf("bad deadline: exit %d, %q", c, se)
	}
	if c, _, _ := run("job-pre", "--deadline", dl); c != 0 || gotPre.GitBase != "https://github.com" {
		t.Errorf("pre: exit %d, opts %+v", c, gotPre)
	}
	if c, _, se := run("job-post", "--deadline", dl); c != 1 || !strings.Contains(se, "not implemented") {
		t.Errorf("post stub: exit %d, %q", c, se)
	}
	if _, so, _ := run("--help"); strings.Contains(so, "job-") {
		t.Errorf("job commands are not hidden:\n%s", so)
	}
}

func TestServeK8sFlags(t *testing.T) {
	const dig = "@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	run := func(args ...string) (ServeOpts, int, string) {
		var got ServeOpts
		h := Handlers{Serve: func(_ context.Context, _ *Env, o ServeOpts) error { got = o; return nil }}
		var stdout, stderr strings.Builder
		code := Run(append([]string{"serve"}, args...), &stdout, &stderr, h, "dev")
		return got, code, stderr.String()
	}
	t.Run("flags", func(t *testing.T) {
		got, code, errs := run("--k8s-namespace", "jobs", "--k8s-instance", "ard", "--k8s-agent-image", "a"+dig,
			"--k8s-pod-image", "p"+dig, "--k8s-resources", "cpu=2", "--k8s-pre-budget", "1m")
		if code != 0 || got.K8s.Namespace != "jobs" || got.K8s.Instance != "ard" || got.K8s.AgentImage != "a"+dig ||
			got.K8s.Resources != "cpu=2" || got.K8s.PreBudget != time.Minute || got.K8s.GitBase != "https://github.com" ||
			got.K8s.ResultGrace != 2*time.Minute {
			t.Fatalf("code %d opts %+v err %q", code, got.K8s, errs)
		}
	})
	t.Run("env fallback", func(t *testing.T) {
		t.Setenv("ARD_K8S_NAMESPACE", "envns")
		t.Setenv("ARD_K8S_POD_IMAGE", "p"+dig)
		got, code, _ := run()
		if code != 0 || got.K8s.Namespace != "envns" || got.K8s.PodImage != "p"+dig {
			t.Fatalf("code %d opts %+v", code, got.K8s)
		}
	})
	t.Run("tag-only image refused", func(t *testing.T) {
		_, code, errs := run("--k8s-agent-image", "agent:latest")
		if code == 0 || !strings.Contains(errs, "digest") {
			t.Fatalf("code %d err %q", code, errs)
		}
	})
}
