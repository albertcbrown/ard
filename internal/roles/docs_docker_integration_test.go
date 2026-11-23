//go:build integration

package roles

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestDocsIntegrationDocker runs the real docs role in its real image under the
// executor's container flags (noexec /tmp, read-only worktree) with no network,
// over the funcs_methods fixture. Needs Docker and the pinned base image.
func TestDocsIntegrationDocker(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if _, _, code := dockerOut(t, 30*time.Second, "info"); code != 0 {
		t.Skip("docker daemon not available")
	}
	tag := fmt.Sprintf("ard-docs-it:%d", time.Now().UnixNano())
	if _, se, code := dockerOut(t, 15*time.Minute, "build", "-q", "-t", tag, "scanners/docs"); code != 0 {
		t.Fatalf("docker build: %s", se)
	}
	t.Cleanup(func() { dockerOut(t, time.Minute, "image", "rm", tag) })
	roleDir, err := filepath.Abs("scanners/docs")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs("testdata/docs/funcs_methods/src")
	if err != nil {
		t.Fatal(err)
	}
	args := scanRunArgs(tag, repo, roleDir, "TMPDIR=/var/tmp")
	args = append([]string{args[0], "--network", "none"}, args[1:]...)
	out, se, code := dockerOut(t, 10*time.Minute, args...)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, se)
	}
	if want := readFile(t, "testdata/docs/funcs_methods/want.jsonl"); out != want {
		t.Errorf("stdout differs from want.jsonl:\n got: %s\nwant: %s", out, want)
	}
}
