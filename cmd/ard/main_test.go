package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "no args prints help", args: nil, wantCode: 0, wantStdout: "Usage:"},
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: "ard dev"},
		{name: "help", args: []string{"help"}, wantCode: 0, wantStdout: "Usage:"},
		{name: "unknown command", args: []string{"nope"}, wantCode: 2, wantStderr: `unknown command "nope"`},
		{name: "config check prints defaults without .ard", args: []string{"config", "check"}, wantCode: 0, wantStdout: "max_pods: 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(tt.args, &stdout, &stderr); got != tt.wantCode {
				t.Errorf("exit code = %d, want %d", got, tt.wantCode)
			}
			if tt.wantStdout != "" && !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

// ard init outside a git repository fails before touching anything, and is
// no longer a stub.
func TestInitOutsideRepoExitsOne(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"init", "--config", filepath.Join(dir, ".ard", "config.yaml")}, &stdout, &stderr)
	if code != 1 || strings.Contains(stderr.String(), "not implemented") || !strings.Contains(stderr.String(), "not the root of a git repository") {
		t.Errorf("code = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".ard")); err == nil {
		t.Error("init wrote .ard/")
	}
}
