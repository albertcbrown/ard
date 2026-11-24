//go:build unix

package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
)

const testDigestRef = "reg.example/ard/agent@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeDocker writes a docker stand-in that logs its argv and fails the
// subcommands listed in failOn ("inspect", "pull", or "all").
func fakeDocker(t *testing.T, failOn ...string) (bin, calls string) {
	t.Helper()
	dir := t.TempDir()
	bin, calls = filepath.Join(dir, "docker"), filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + calls + "\n"
	for _, f := range failOn {
		if f == "all" {
			script += "exit 1\n"
		} else {
			script += "[ \"$1\" = image ] && [ \"$2\" = " + f + " ] && exit 1\n[ \"$1\" = " + f + " ] && exit 1\n"
		}
	}
	script += "exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func (f *fixture) withAgentImage(t *testing.T, bin, ref string) {
	t.Helper()
	o := f.opts
	o.DockerBin, o.AgentImage = bin, ref
	l, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	f.l = l
}

func TestNewRejectsBadAgentImage(t *testing.T) {
	for _, ref := range []string{"ard-agent:dev", "-x", "a b", "sha256:abc"} {
		if _, err := New(Options{Root: t.TempDir(), AgentImage: ref}); err == nil {
			t.Errorf("%q accepted", ref)
		}
	}
}

func TestPrepareDefaultImage(t *testing.T) {
	f := newFixture(t)
	bin, calls := fakeDocker(t)
	f.withAgentImage(t, bin, "sha256:0123456789ab")
	h, err := f.l.Prepare(context.Background(), f.job(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	defer f.l.Cleanup(context.Background(), h, executor.CleanupOptions{})
	if h.Kind != "docker" || h.Image != "sha256:0123456789ab" || h.Container != "ard-"+h.JobID {
		t.Errorf("handle = %+v", h)
	}
	if h.View.Work != "/work" || h.View.Role != "/role" || h.View.ConfigDir != "/ard-config" {
		t.Errorf("view = %+v", h.View)
	}
	b, _ := os.ReadFile(calls)
	if strings.Contains(string(b), "pull") {
		t.Errorf("local image was pulled: %s", b)
	}
}

func TestPrepareDockerfileWins(t *testing.T) {
	f := newFixture(t)
	bin, _ := fakeDocker(t)
	f.withAgentImage(t, bin, testDigestRef)
	os.WriteFile(filepath.Join(f.roleDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	j := f.job(time.Minute)
	j.Dockerfile = filepath.Join(f.roleDir, "Dockerfile")
	h, err := f.l.Prepare(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	defer f.l.Cleanup(context.Background(), h, executor.CleanupOptions{})
	if h.Kind != "docker" || !strings.HasPrefix(h.Image, "ard-role-") {
		t.Errorf("handle = %+v", h)
	}
}

func TestPrepareTrustedScannerStaysHost(t *testing.T) {
	f := newFixture(t)
	bin, calls := fakeDocker(t)
	f.withAgentImage(t, bin, testDigestRef)
	j := f.job(time.Minute)
	j.Stage, j.TrustedInput = "scanner", true
	h, err := f.l.Prepare(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	defer f.l.Cleanup(context.Background(), h, executor.CleanupOptions{})
	if h.Kind != "host" {
		t.Errorf("kind = %q", h.Kind)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Error("docker was called")
	}
}

func TestPrepareUnsetUnchanged(t *testing.T) {
	f := newFixture(t)
	h, err := f.l.Prepare(context.Background(), f.job(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	defer f.l.Cleanup(context.Background(), h, executor.CleanupOptions{})
	if h.Kind != "host" {
		t.Errorf("kind = %q", h.Kind)
	}
}

func TestPrepareDockerDownNoFallback(t *testing.T) {
	f := newFixture(t)
	bin, _ := fakeDocker(t, "all")
	f.withAgentImage(t, bin, testDigestRef)
	j := f.job(time.Minute)
	h, err := f.l.Prepare(context.Background(), j)
	if err == nil {
		t.Fatal("Prepare succeeded with Docker down")
	}
	if h.Kind == "host" {
		t.Error("fell back to host mode")
	}
	if _, err := os.Stat(mustWT(t, f.l, j)); !os.IsNotExist(err) {
		t.Error("worktree left behind")
	}
}

func TestEnsurePulled(t *testing.T) {
	ctx := context.Background()
	log := filepath.Join(t.TempDir(), "log")
	os.WriteFile(log, nil, 0o600)

	bin, calls := fakeDocker(t)
	l, _ := New(Options{Root: t.TempDir(), DockerBin: bin})
	if err := l.ensurePulled(ctx, testDigestRef, log); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(calls); strings.Contains(string(b), "pull") {
		t.Errorf("present image pulled: %s", b)
	}

	bin, calls = fakeDocker(t, "inspect")
	l, _ = New(Options{Root: t.TempDir(), DockerBin: bin})
	if err := l.ensurePulled(ctx, testDigestRef, log); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(calls); !strings.Contains(string(b), "pull "+testDigestRef) {
		t.Errorf("no pull: %s", b)
	}

	bin, _ = fakeDocker(t, "inspect", "pull")
	l, _ = New(Options{Root: t.TempDir(), DockerBin: bin})
	if err := l.ensurePulled(ctx, testDigestRef, log); err == nil {
		t.Error("pull failure not reported")
	}
	if err := l.ensurePulled(ctx, "sha256:0123456789ab", log); err == nil {
		t.Error("missing local image ID not reported")
	}
}

func TestRunArgvBuildvcsFlag(t *testing.T) {
	l, _ := New(Options{Root: "/r"})
	h := executor.Handle{JobID: "J", Container: "ard-J", Image: "sha256:0123456789ab", Worktree: "/r/w", RoleDir: "/role",
		RepoDir: "/r/repos/o/r.git"}
	joined := strings.Join(l.runArgv(h, executor.Command{Path: "/role/x"}, false), " ")
	for _, w := range []string{"GOFLAGS=-buildvcs=false", "--cap-drop ALL", "no-new-privileges"} {
		if !strings.Contains(joined, w) {
			t.Errorf("missing %q", w)
		}
	}
	if strings.Contains(joined, h.RepoDir) {
		t.Errorf("controller clone mounted: %s", joined)
	}
}
