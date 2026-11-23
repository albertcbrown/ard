//go:build integration

package roles

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Runs scan inside its pinned image the way the local executor does and
// compares with the host run, so GNU tools in the image and BSD tools on a Mac
// must give the same output. Needs Docker and network (jq download).
func TestErrCodesDockerMatchesHost(t *testing.T) {
	needTools(t, "docker", "sh", "jq")
	e := ecSetup(t, baseCodes)
	e.cleanRepo(t)
	e.write(t, "b.go", "E7777 E1001\n")
	e.write(t, "reg.txt", "E1001 = a\nE1002 = b\nE1001 = c\n")
	want, errOut, code := e.run(t)
	if code != 0 || want == "" {
		t.Fatalf("host run: %d %s", code, errOut)
	}
	if _, se, c := dockerOut(t, 15*time.Minute, "build", "-q", "-t", "ard-errcodes-test", "scanners/error-codes"); c != 0 {
		t.Fatalf("docker build: %s", se)
	}
	roleDir, _ := filepath.Abs(e.role)
	if err := os.Chmod(roleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	got, se, c := dockerOut(t, 5*time.Minute, "run", "--rm", "--read-only", "--network", "none",
		"--user", "65534", "--cap-drop", "ALL", "--tmpfs", "/tmp:rw,noexec",
		"-v", e.repo+":/repo:ro", "-v", roleDir+":/role:ro", "-w", "/repo", "ard-errcodes-test", "/role/scan")
	if c != 0 || got != want {
		t.Fatalf("docker run exit %d\n got: %s\nwant: %s\nstderr: %s", c, got, want, se)
	}
}
