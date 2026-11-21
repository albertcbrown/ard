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

	if err := Check(context.Background(), e); err != nil {
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

	if err := Check(context.Background(), e); err != nil {
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
