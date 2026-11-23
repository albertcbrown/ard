//go:build integration

package roles

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestTrufflehogDocker runs the role's own image under the executor's flags
// plus --network none. In the container the symlink dangles; only the host
// test proves symlinks are not followed. Needs Docker and network (to build).
func TestTrufflehogDocker(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if _, _, code := dockerOut(t, 30*time.Second, "info"); code != 0 {
		t.Skip("docker daemon not available")
	}
	// With the binary set, it must be the pinned one (fails otherwise).
	var hostBin string
	if os.Getenv("ARD_TRUFFLEHOG_BIN") != "" {
		hostBin = thiBin(t)
	}
	roleDir, err := filepath.Abs("scanners/trufflehog")
	if err != nil {
		t.Fatal(err)
	}
	tag := fmt.Sprintf("ard-trufflehog-it:%d", time.Now().UnixNano())
	if _, se, code := dockerOut(t, 20*time.Minute, "build", "-q", "-t", tag, "scanners/trufflehog"); code != 0 {
		t.Fatalf("docker build: %s", se)
	}
	t.Cleanup(func() { dockerOut(t, time.Minute, "image", "rm", tag) })

	repo, p := thiPlantRepo(t)
	hostTmp := t.TempDir()
	args := append([]string{"run", "--network", "none"}, scanRunArgs(tag, repo, roleDir)[1:]...)
	// Insert the /var/tmp mount before the image (the last two args are image, script).
	n := len(args) - 2
	args = append(append(append([]string{}, args[:n]...), "-v", hostTmp+":/var/tmp"), args[n:]...)

	so, se, code := dockerOut(t, 20*time.Minute, args...)
	if code != 0 {
		t.Fatalf("exit %d\nstderr:\n%s", code, se)
	}
	thiCheckFindings(t, []byte(so))
	if h := thiLeaks(p, []byte(so)); len(h) > 0 {
		t.Errorf("planted value(s) %v in stdout", h)
	}
	if h := thiLeaks(p, []byte(se)); len(h) > 0 {
		t.Errorf("planted value(s) %v in stderr", h)
	}
	thiLeaksInDir(t, p, hostTmp)
	if hostBin != "" {
		env, _, _ := thiHostEnv(t, hostBin)
		hostOut, _, hcode := thiRunScan(t, repo, env)
		if hcode != 0 || !bytes.Equal(hostOut, []byte(so)) {
			t.Errorf("Docker stdout differs from a host run (host exit %d)", hcode)
		}
	}
	so2, _, code2 := dockerOut(t, 20*time.Minute, args...)
	if code2 != 0 || so2 != so {
		t.Errorf("second run differs (exit %d)", code2)
	}
}
