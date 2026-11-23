//go:build integration

package roles

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMissingTestsIntegrationDocker runs the real missing-tests role in its
// real image under the executor's container flags (noexec /tmp, read-only
// worktree) over the basic fixture. Needs Docker and the pinned base image.
func TestMissingTestsIntegrationDocker(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if _, _, code := dockerOut(t, 30*time.Second, "info"); code != 0 {
		t.Skip("docker daemon not available")
	}
	tag := fmt.Sprintf("ard-missing-tests-it:%d", time.Now().UnixNano())
	if _, se, code := dockerOut(t, 15*time.Minute, "build", "-q", "-t", tag, "scanners/missing-tests"); code != 0 {
		t.Fatalf("docker build: %s", se)
	}
	t.Cleanup(func() { dockerOut(t, time.Minute, "image", "rm", tag) })
	roleDir, err := filepath.Abs("scanners/missing-tests")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs("testdata/missing-tests/basic")
	if err != nil {
		t.Fatal(err)
	}
	args := scanRunArgs(tag, repo, roleDir, "TMPDIR=/var/tmp")
	args = append([]string{args[0], "--network", "none"}, args[1:]...)
	out, se, code := dockerOut(t, 10*time.Minute, args...)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, se)
	}
	// The fixture's want.jsonl records the version as go1.test.
	var ver string
	for _, l := range strings.Split(se, "\n") {
		if v, ok := strings.CutPrefix(l, "missing-tests: go1"); ok && !strings.Contains(l, "module") {
			ver = "go1" + v
			break
		}
	}
	want := strings.ReplaceAll(readFile(t, "testdata/missing-tests/basic/want.jsonl"), "go1.test", ver)
	if out != want {
		t.Errorf("stdout differs from want.jsonl:\n got: %s\nwant: %s", out, want)
	}
}
