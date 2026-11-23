//go:build integration

package roles

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIntegrationLintDocker builds scanners/lint and runs the role under the
// executor's container flags against a go 1.27 module (findings) and a go 1.99
// module (exit 1 with the newer-Go message). Needs Docker and network.
func TestIntegrationLintDocker(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if _, _, code := dockerOut(t, 30*time.Second, "info"); code != 0 {
		t.Skip("docker daemon not available")
	}
	tag := fmt.Sprintf("ard-lint-it:%d", time.Now().UnixNano())
	if _, se, code := dockerOut(t, 20*time.Minute, "build", "-q", "-t", tag, "scanners/lint"); code != 0 {
		t.Fatalf("docker build: %s", se)
	}
	t.Cleanup(func() { dockerOut(t, time.Minute, "image", "rm", tag) })
	role := lintRoleCopy(t, strp(""))

	repo := t.TempDir()
	lintFixtureModule(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/lintfixture\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, se, code := dockerOut(t, 20*time.Minute, scanRunArgs(tag, repo, role)...)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, se)
	}
	for _, w := range []string{"lint:errcheck:main.go:", "lint:govet/printf:main.go:", "lint:unused:main.go:", "lint:nolint:main.go:"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %s in:\n%s", w, out)
		}
	}

	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/lintfixture\n\ngo 1.99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, se, code = dockerOut(t, 20*time.Minute, scanRunArgs(tag, repo, role)...)
	if code != 1 || out != "" || !strings.Contains(se, "go.mod needs a newer Go than the image's") {
		t.Fatalf("go 1.99: exit %d stdout %q\n%s", code, out, se)
	}
}
