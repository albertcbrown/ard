package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime/debug"
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

func TestResolveVersion(t *testing.T) {
	info := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: v}}
	}
	tests := []struct {
		name string
		v    string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"ldflag wins", "v1.2.3", info("v9.9.9"), true, "v1.2.3"},
		{"build info tag", "dev", info("v1.4.0"), true, "v1.4.0"},
		{"devel", "dev", info("(devel)"), true, "dev"},
		{"empty", "dev", info(""), true, "dev"},
		{"no build info", "dev", nil, false, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveVersion(tt.v, tt.info, tt.ok); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
