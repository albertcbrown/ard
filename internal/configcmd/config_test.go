package configcmd

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/executor/local"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

func testEnv(t *testing.T, configPath string) (*cli.Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	e := &cli.Env{
		Globals: cli.Globals{ConfigPath: configPath},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	return e, &stdout, &stderr
}

// writeRepo materialises a repository with .ard/config.yaml and one scanner
// role, returning its root.
func writeRepo(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, data string, mode os.FileMode) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), mode); err != nil {
			t.Fatal(err)
		}
	}
	mk(".ard/config.yaml", config, 0o644)
	mk(".ard/scanners/cve/scan", "#!/bin/sh\n", 0o755)
	mk(".ard/triage/lead/prompt.txt", "route the issue", 0o644)
	return root
}

func TestCheckValid(t *testing.T) {
	root := writeRepo(t, "max_pods: 5\noverrides: {scanners/cve: {every: 6h}}\n")
	e, stdout, stderr := testEnv(t, filepath.Join(root, ".ard", "config.yaml"))

	if err := Check(context.Background(), e, cli.ConfigCheckOpts{}); err != nil {
		t.Fatalf("Check: %v (stderr: %s)", err, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"max_pods: 5", "executor: local", "every: 6h", "name: cve", "dir: .ard/scanners/cve"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}
	// The fixture's triage role has no Dockerfile, so the host warning is the
	// only thing on stderr.
	if want := local.HostRoleText("triage", "lead"); stderr.String() != want {
		t.Errorf("stderr = %q, want only the host warning %q", stderr.String(), want)
	}
}

func TestCheckWarnsHostUntrustedAtErrorLevel(t *testing.T) {
	root := writeRepo(t, "max_pods: 5\n")
	e, _, stderr := testEnv(t, filepath.Join(root, ".ard", "config.yaml"))
	var logs bytes.Buffer
	e.Log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := Check(context.Background(), e, cli.ConfigCheckOpts{}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if strings.Contains(logs.String(), "executor.host_untrusted") {
		t.Errorf("logs = %q, want no warning at error level", logs.String())
	}
	out := stderr.String()
	if n := strings.Count(out, "WARNING:"); n != 1 || !strings.Contains(out, `role "lead"`) {
		t.Errorf("stderr = %q, want exactly one WARNING for role lead", out)
	}
}

func TestCheckWarnsHostUntrusted(t *testing.T) {
	count := func(root string) (string, string) {
		e, _, stderr := testEnv(t, filepath.Join(root, ".ard", "config.yaml"))
		var logs bytes.Buffer
		e.Log = slog.New(slog.NewTextHandler(&logs, nil))
		if err := Check(context.Background(), e, cli.ConfigCheckOpts{}); err != nil {
			t.Fatalf("Check: %v", err)
		}
		return logs.String(), stderr.String()
	}
	root := writeRepo(t, "max_pods: 5\n")
	logs, errs := count(root)
	if n := strings.Count(logs, "executor.host_untrusted"); n != 1 || !strings.Contains(logs, "role=lead") || strings.Contains(logs, "role=cve") {
		t.Errorf("logs = %q, want one warning for lead only", logs)
	}
	if n := strings.Count(errs, "WARNING:"); n != 1 {
		t.Errorf("stderr = %q, want one warning", errs)
	}
	if err := os.WriteFile(filepath.Join(root, ".ard", "triage", "lead", "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logs, errs = count(root)
	if strings.Contains(logs, "executor.host_untrusted") || errs != "" {
		t.Errorf("Dockerfile role warned: logs %q stderr %q", logs, errs)
	}
}

func TestCheckDefaultsWithoutConfig(t *testing.T) {
	root := writeRepo(t, "")
	cfgPath := filepath.Join(root, ".ard", "config.yaml")
	if err := os.Remove(cfgPath); err != nil {
		t.Fatal(err)
	}
	e, stdout, _ := testEnv(t, cfgPath)

	if err := Check(context.Background(), e, cli.ConfigCheckOpts{}); err != nil {
		t.Fatalf("Check without a config file must succeed with defaults: %v", err)
	}
	if !strings.Contains(stdout.String(), "max_pods: 3") {
		t.Errorf("stdout missing defaults:\n%s", stdout.String())
	}
}

func TestCheckReportsErrors(t *testing.T) {
	root := writeRepo(t, "executor: remote\noverrides: {scanners/cve: {max_open: 0}}\n")

	// Route through cli.Run: the CLI layer prints the returned errors to
	// stderr and maps them to exit code 1.
	var stdout, stderr bytes.Buffer
	code := cli.Run(
		[]string{"config", "check", "--config", filepath.Join(root, ".ard", "config.yaml")},
		&stdout, &stderr,
		cli.Handlers{ConfigCheck: Check}, "dev",
	)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	errOut := stderr.String()
	for _, want := range []string{
		".ard/config.yaml: executor:",
		`.ard/config.yaml: overrides["scanners/cve"].max_open:`,
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q:\n%s", want, errOut)
		}
	}
	// The resolved config still prints to stdout for inspection.
	if !strings.Contains(stdout.String(), "executor: remote") {
		t.Errorf("stdout missing resolved config:\n%s", stdout.String())
	}
}

// gitRepo commits the files (rel path -> content) to a fresh repo and returns
// its root. A "*" prefix on the path marks the file executable.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	_, clone := gittest.NewRemote(t)
	for rel, content := range files {
		mode := os.FileMode(0o644)
		if strings.HasPrefix(rel, "*") {
			rel, mode = rel[1:], 0o755
		}
		p := filepath.Join(clone, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "-m", "config")
	return clone
}

func TestCheckRef(t *testing.T) {
	good := map[string]string{".ard/config.yaml": "max_pods: 5\n", "*.ard/scanners/cve/scan": "#!/bin/sh\n"}
	tests := []struct {
		name       string
		committed  map[string]string
		dirty      string // overwrites .ard/config.yaml in the working tree
		ref        string
		wantExit   int
		wantStdout []string
		wantStderr []string
	}{
		{
			name: "valid commit, dirty invalid working tree", committed: good, dirty: "executor: bogus\n", ref: "HEAD",
			wantStdout: []string{"max_pods: 5", "sha: ", "name: cve"},
		},
		{
			name: "bad ref", committed: good, ref: "no-such-ref", wantExit: 1,
			wantStderr: []string{"resolve --ref no-such-ref"},
		},
		{
			name: "invalid config at the ref", committed: map[string]string{".ard/config.yaml": "executor: remote\n"}, ref: "HEAD", wantExit: 1,
			wantStdout: []string{"sha: "},
			wantStderr: []string{".ard/config.yaml: executor:"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := gitRepo(t, tc.committed)
			if tc.dirty != "" {
				if err := os.WriteFile(filepath.Join(root, ".ard", "config.yaml"), []byte(tc.dirty), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			code := cli.Run(
				[]string{"config", "check", "--config", filepath.Join(root, ".ard", "config.yaml"), "--ref", tc.ref},
				&stdout, &stderr, cli.Handlers{ConfigCheck: Check}, "dev",
			)
			if code != tc.wantExit {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, tc.wantExit, stderr.String())
			}
			for _, w := range tc.wantStdout {
				if !strings.Contains(stdout.String(), w) {
					t.Errorf("stdout missing %q:\n%s", w, stdout.String())
				}
			}
			for _, w := range tc.wantStderr {
				if !strings.Contains(stderr.String(), w) {
					t.Errorf("stderr missing %q:\n%s", w, stderr.String())
				}
			}
			if code == 0 && !strings.Contains(stdout.String(), "repo: "+root) {
				t.Errorf("output names the temp dir rather than the repo:\n%s", stdout.String())
			}
		})
	}
}

// Without --ref the output is exactly what it was before the flag existed:
// no sha line, and the working tree is what is checked.
func TestCheckWithoutRefIsUnchanged(t *testing.T) {
	root := gitRepo(t, map[string]string{".ard/config.yaml": "max_pods: 5\n"})
	if err := os.WriteFile(filepath.Join(root, ".ard", "config.yaml"), []byte("max_pods: 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, stdout, _ := testEnv(t, filepath.Join(root, ".ard", "config.yaml"))
	if err := Check(context.Background(), e, cli.ConfigCheckOpts{}); err != nil {
		t.Fatal(err)
	}
	want, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var wantOut bytes.Buffer
	if err := want.WriteYAML(&wantOut); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != wantOut.String() || strings.Contains(stdout.String(), "sha:") || !strings.Contains(stdout.String(), "max_pods: 7") {
		t.Errorf("stdout differs from the plain working-tree render:\n%s", stdout.String())
	}
}

func TestCheckIntakeLabel(t *testing.T) {
	tests := []struct {
		name       string
		cfg        string
		wantExit   int
		wantStderr []string
		noStderr   []string
	}{
		{"own label", `intake: {mode: label, label: "ard:needs-human"}`, 1, []string{".ard/config.yaml: intake.label:", "uses itself"}, nil},
		{"stage label", `intake: {mode: label, label: "ard:triage:lead:done"}`, 1, []string{".ard/config.yaml: intake.label:", "uses itself"}, nil},
		{"custom", `intake: {mode: label, label: "ard:intake"}`, 0, nil, nil},
		{"label default", `intake: {mode: label}`, 0, nil, nil},
		{"all", `intake: {mode: all}`, 0, nil, nil},
		{"all with label", `intake: {mode: all, label: "ard:needs-human"}`, 1, []string{"only valid when intake.mode is label"}, []string{"uses itself"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeRepo(t, tc.cfg+"\n")
			var stdout, stderr bytes.Buffer
			code := cli.Run(
				[]string{"config", "check", "--config", filepath.Join(root, ".ard", "config.yaml")},
				&stdout, &stderr, cli.Handlers{ConfigCheck: Check}, "dev",
			)
			if code != tc.wantExit {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, tc.wantExit, stderr.String())
			}
			if !strings.Contains(stdout.String(), "intake:") {
				t.Errorf("stdout lacks the resolved config:\n%s", stdout.String())
			}
			for _, w := range tc.wantStderr {
				if !strings.Contains(stderr.String(), w) {
					t.Errorf("stderr missing %q:\n%s", w, stderr.String())
				}
			}
			for _, w := range tc.noStderr {
				if strings.Contains(stderr.String(), w) {
					t.Errorf("stderr has %q:\n%s", w, stderr.String())
				}
			}
		})
	}

	t.Run("ref", func(t *testing.T) {
		root := gitRepo(t, map[string]string{
			".ard/config.yaml":        `intake: {mode: label, label: "ard:needs-human"}` + "\n",
			"*.ard/scanners/cve/scan": "#!/bin/sh\n",
		})
		var stdout, stderr bytes.Buffer
		code := cli.Run(
			[]string{"config", "check", "--config", filepath.Join(root, ".ard", "config.yaml"), "--ref", "HEAD"},
			&stdout, &stderr, cli.Handlers{ConfigCheck: Check}, "dev",
		)
		if code != 1 || !strings.Contains(stderr.String(), "intake.label:") {
			t.Errorf("exit = %d, stderr: %s", code, stderr.String())
		}
	})
}

func TestCheckRefusesOldGit(t *testing.T) {
	shim := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\necho 'git version 2.39.5'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitBin = shim
	t.Cleanup(func() { gitBin = "" })
	e, stdout, _ := testEnv(t, t.TempDir())
	err := Check(context.Background(), e, cli.ConfigCheckOpts{})
	if err == nil || !strings.Contains(err.Error(), "ard config check:") || !strings.Contains(err.Error(), "2.39.5") {
		t.Fatalf("err = %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestCheckWarnsWhenEveryReviewerIsGated(t *testing.T) {
	const warn = "every reviewer has a when: rule"
	for _, tt := range []struct {
		name, cfg string
		want      bool
	}{
		{"all gated", "overrides: {reviewers/a: {when: {paths: [go.mod]}}, reviewers/b: {when: {paths: [web/]}}}\n", true},
		{"one ungated", "overrides: {reviewers/a: {when: {paths: [go.mod]}}}\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := writeRepo(t, tt.cfg)
			for _, n := range []string{"a", "b"} {
				p := filepath.Join(root, ".ard", "reviewers", n, "prompt.txt")
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("review"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			e, _, stderr := testEnv(t, filepath.Join(root, ".ard", "config.yaml"))
			if err := Check(context.Background(), e, cli.ConfigCheckOpts{}); err != nil {
				t.Fatalf("Check: %v", err)
			}
			if got := strings.Contains(stderr.String(), warn); got != tt.want {
				t.Errorf("warning present = %v, want %v; stderr: %s", got, tt.want, stderr.String())
			}
		})
	}
}
