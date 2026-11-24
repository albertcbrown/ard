//go:build integration && unix

package local

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
)

func TestDockerRun(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker unavailable")
	}
	f := newFixture(t)
	df, _ := filepath.Abs("testdata/Dockerfile")
	os.WriteFile(filepath.Join(f.roleDir, "Dockerfile"), mustRead(t, df), 0o644)
	os.WriteFile(filepath.Join(f.roleDir, "run.sh"), []byte("#!/bin/sh\necho out; echo err >&2; echo x > /work/probe.txt\n"), 0o755)
	ctx := context.Background()

	for _, ro := range []bool{false, true} {
		j := f.job(time.Minute)
		j.Dockerfile = filepath.Join(f.roleDir, "Dockerfile")
		j.ReadOnly = ro
		h, st := f.run(t, j, executor.Command{Path: "/role/run.sh"})
		if st.State != executor.Exited {
			t.Fatalf("%+v", st)
		}
		if h.View.ConfigDir != "/ard-config" {
			t.Errorf("View.ConfigDir = %q, want /ard-config", h.View.ConfigDir)
		}
		if got := readFile(t, h.StdoutPath); !strings.Contains(got, "out") || strings.Contains(got, "err") {
			t.Errorf("stdout %q", got)
		}
		_, werr := os.Stat(filepath.Join(h.Worktree, "probe.txt"))
		if ro && (st.ExitCode == 0 || werr == nil) {
			t.Error("write to read-only /work succeeded")
		}
		if !ro && (st.ExitCode != 0 || werr != nil) {
			t.Errorf("write failed: %+v", st)
		}
		f.l.Cleanup(ctx, h, executor.CleanupOptions{})
		if out, _ := exec.Command("docker", "ps", "-aq", "--filter", "name="+h.Container).Output(); len(strings.TrimSpace(string(out))) > 0 {
			t.Error("container left behind")
		}
	}

	// A sidecar is mounted read-only at /sidecar.
	sf := newSidecarFixture(t)
	sf.push(t, map[string]string{"spec.md": "a"})
	os.WriteFile(filepath.Join(sf.roleDir, "Dockerfile"), mustRead(t, df), 0o644)
	os.WriteFile(filepath.Join(sf.roleDir, "sc.sh"), []byte("#!/bin/sh\ncat /sidecar/spec.md && ! touch /sidecar/x\n"), 0o755)
	sj := sf.sjob("main")
	sj.Dockerfile = filepath.Join(sf.roleDir, "Dockerfile")
	sh, sst := sf.run(t, sj, executor.Command{Path: "/role/sc.sh"})
	if sst.State != executor.Exited || sst.ExitCode != 0 || !strings.Contains(readFile(t, sh.StdoutPath), "a") {
		t.Errorf("sidecar in docker: %+v stdout %q", sst, readFile(t, sh.StdoutPath))
	}
	if sh.View.Sidecar != "/sidecar" {
		t.Errorf("View.Sidecar = %q", sh.View.Sidecar)
	}

	j := f.job(time.Second)
	j.Dockerfile = filepath.Join(f.roleDir, "Dockerfile")
	os.WriteFile(filepath.Join(f.roleDir, "sleep.sh"), []byte("#!/bin/sh\nsleep 300\n"), 0o755)
	h, st := f.run(t, j, executor.Command{Path: "/role/sleep.sh"})
	if st.State != executor.TimedOut {
		t.Fatalf("%+v", st)
	}
	f.l.Cleanup(ctx, h, executor.CleanupOptions{})
	if out, _ := exec.Command("docker", "ps", "-aq", "--filter", "name="+h.Container).Output(); len(strings.TrimSpace(string(out))) > 0 {
		t.Error("container left after timeout")
	}
}

func mustRead(t *testing.T, p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDockerHistory(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker unavailable")
	}
	f := newFixture(t)
	sh(t, f.repo, "git", "commit", "-q", "--allow-empty", "-m", "second")
	bare := filepath.Join(t.TempDir(), "bare.git")
	sh(t, f.repo, "git", "clone", "-q", "--bare", f.repo, bare)
	df := filepath.Join(f.roleDir, "Dockerfile")
	os.WriteFile(df, []byte("FROM alpine:3\nRUN apk add --no-cache git\n"), 0o644)
	os.WriteFile(filepath.Join(f.roleDir, "hist.sh"), []byte("#!/bin/sh\n"+
		"set -e\n[ \"$ARD_HISTORY\" = /history ]\n"+
		"[ \"$(git --git-dir /history log --all --format=%H | wc -l)\" -gt 1 ]\n"+
		"! touch /history/probe\n"), 0o755)
	j := f.job(2 * time.Minute)
	j.Stage, j.Role, j.History, j.RepoDir, j.Dockerfile = "scanner", "hist", true, bare, df
	h, st := f.run(t, j, executor.Command{Path: "/role/hist.sh"})
	if st.State != executor.Exited || st.ExitCode != 0 {
		t.Fatalf("%+v stderr %q", st, readFile(t, h.StdoutPath))
	}
	if _, err := os.Stat(filepath.Join(bare, "probe")); err == nil {
		t.Error("write to read-only /history reached the host")
	}
	f.l.Cleanup(context.Background(), h, executor.CleanupOptions{})
}
