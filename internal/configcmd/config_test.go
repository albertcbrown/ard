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
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
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
