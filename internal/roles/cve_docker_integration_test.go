//go:build integration

package roles

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// An older base image than scanners/cve/Dockerfile's, so a go 1.27 module is
// "newer than the image" while 1.27.4 is still the newest Go release. Digest
// from `docker buildx imagetools inspect`, 2026-11-21.
const oldGoImage = "golang:1.26.11-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0"

func dockerOut(t *testing.T, timeout time.Duration, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("docker %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return so.String(), se.String(), code
}

// scanRunArgs mirrors the executor's runArgv for a read-only scanner job.
// Keep in sync with internal/executor/local/docker.go runArgv.
func scanRunArgs(image, repo, roleDir string, extraEnv ...string) []string {
	a := []string{"run", "--rm", "--init",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--tmpfs", "/tmp",
		"-v", repo + ":/work:ro", "-v", roleDir + ":/role:ro",
		"-w", "/work", "-e", "HOME=/tmp", "-e", "LANG=C.UTF-8", "-e", "GIT_TERMINAL_PROMPT=0"}
	for _, kv := range extraEnv {
		a = append(a, "-e", kv)
	}
	return append(a, image, "/role/scan")
}

func buildCVEImage(t *testing.T, tag string, buildArgs ...string) {
	t.Helper()
	args := append([]string{"build", "-q", "-t", tag}, buildArgs...)
	args = append(args, "scanners/cve")
	if _, se, code := dockerOut(t, 15*time.Minute, args...); code != 0 {
		t.Fatalf("docker build: %s", se)
	}
	t.Cleanup(func() { dockerOut(t, time.Minute, "image", "rm", tag) })
}

// TestIntegrationDocker runs the real cve role in its real image under the
// executor's container flags (noexec /tmp, read-only worktree) against a
// module whose go directive is newer than the image's Go. Needs Docker, a
// host Go >= 1.27 and network.
func TestIntegrationDocker(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if _, _, code := dockerOut(t, 30*time.Second, "info"); code != 0 {
		t.Skip("docker daemon not available")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	repo := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/vulnfixture\n\ngo 1.27\n\nrequire golang.org/x/text v0.3.7\n")
	write("main.go", `package main

import "golang.org/x/text/language"

func main() { _, _, _ = language.ParseAcceptLanguage("en") }
`)
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = repo
	tidy.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOFLAGS=-mod=mod")
	if out, err := tidy.CombinedOutput(); err != nil {
		if bytes.Contains(out, []byte("requires go >=")) {
			t.Skipf("host Go is older than 1.27: %s", out)
		}
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	roleDir, err := filepath.Abs("scanners/cve")
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UnixNano()
	oldTag := fmt.Sprintf("ard-cve-it-old:%d", stamp)
	buildCVEImage(t, oldTag, "--build-arg", "GO_IMAGE="+oldGoImage)
	const want = "osv:GHSA-69ch-w2m2-3vjp:go.mod:golang.org/x/text"

	t.Run("switch", func(t *testing.T) {
		out, se, code := dockerOut(t, 20*time.Minute, scanRunArgs(oldTag, repo, roleDir)...)
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, se)
		}
		if !regexp.MustCompile(`building v1\.10\.0 with go1\.27`).MatchString(se) {
			t.Errorf("no toolchain switch in the log:\n%s", se)
		}
		var found bool
		for _, f := range decode(t, []byte(out)) {
			if f.Fingerprint == want {
				found = true
				if !strings.Contains(f.Body, "Reachable: yes") || !strings.Contains(f.Body, "Go toolchain: `go1.27") {
					t.Errorf("unexpected finding:\n%s", f.Body)
				}
			}
		}
		if !found {
			t.Fatalf("missing %s:\n%s", want, out)
		}
	})

	t.Run("noexec", func(t *testing.T) {
		out, se, code := dockerOut(t, 20*time.Minute, scanRunArgs(oldTag, repo, roleDir, "TMPDIR=/tmp")...)
		if code != 1 || out != "" || !strings.Contains(se, "noexec") {
			t.Fatalf("exit %d stdout %q\n%s", code, out, se)
		}
	})

	t.Run("pinned", func(t *testing.T) {
		pinTag := fmt.Sprintf("ard-cve-it-pinned:%d", stamp)
		buildCVEImage(t, pinTag)
		out, se, code := dockerOut(t, 20*time.Minute, scanRunArgs(pinTag, repo, roleDir)...)
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, se)
		}
		if strings.Contains(se, "building") || strings.Contains(out, "Go toolchain:") {
			t.Errorf("pinned image switched toolchains:\n%s", se)
		}
		if !strings.Contains(out, want) {
			t.Errorf("missing %s:\n%s", want, out)
		}
	})
}
