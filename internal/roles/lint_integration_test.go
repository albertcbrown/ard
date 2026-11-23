//go:build integration

package roles

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// lintRoleCopy copies scanners/lint into a temp role directory with the given
// baseline (nil: none), the way a repo would carry it.
func lintRoleCopy(t *testing.T, baseline *string) string {
	t.Helper()
	role := t.TempDir()
	for _, n := range []string{"scan", "normalize.jq"} {
		b, err := os.ReadFile(filepath.Join("scanners", "lint", n))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(role, n), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if baseline != nil {
		if err := os.WriteFile(filepath.Join(role, "baseline"), []byte(*baseline), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return role
}

func lintFixtureModule(t *testing.T, repo string) {
	t.Helper()
	files := map[string]string{
		"go.mod": "module example.com/lintfixture\n\ngo 1.21\n",
		"main.go": `package main

import (
	"fmt"
	"os"
)

func unusedHelper() {}

func main() {
	os.Remove("a")
	fmt.Printf("%d\n", "x")
	os.Remove("b") //nolint:errcheck
}
`,
		// issues.new would hide everything in a clean checkout; the forced flags win.
		".golangci.yml": "version: \"2\"\nissues:\n  new: true\n",
	}
	for n, s := range files {
		if err := os.WriteFile(filepath.Join(repo, n), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runLintHost(t *testing.T, repo, role string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{filepath.Join(role, "scan")}, args...)...)
	cmd.Dir = repo
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return so.String(), se.String(), code
}

// TestIntegrationLintHost runs the real scan with the host's golangci-lint and
// go: findings appear, a generated baseline silences them, and one new
// unchecked call is exactly one new finding.
func TestIntegrationLintHost(t *testing.T) {
	needTools(t, "golangci-lint", "go", "jq", "sh")
	repo := t.TempDir()
	lintFixtureModule(t, repo)
	role := lintRoleCopy(t, strp(""))

	out, se, code := runLintHost(t, repo, role)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, se)
	}
	for _, w := range []string{"lint:errcheck:main.go:", "lint:govet/printf:main.go:", "lint:unused:main.go:", "lint:nolint:main.go:"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %s in:\n%s", w, out)
		}
	}

	base, se, code := runLintHost(t, repo, role, "--baseline")
	if code != 0 {
		t.Fatalf("--baseline exit %d\n%s", code, se)
	}
	if err := os.WriteFile(filepath.Join(role, "baseline"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, se, code = runLintHost(t, repo, role); code != 0 || out != "" {
		t.Fatalf("with baseline: exit %d stdout %q\n%s", code, out, se)
	}

	// One new unchecked call with a message the baseline lacks (an identical
	// message in the same file would collapse into a baselined finding).
	src, err := os.ReadFile(filepath.Join(repo, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	src = bytes.Replace(src, []byte("func unusedHelper() {}"), []byte("func unusedHelper() { os.Setenv(\"a\", \"b\") }"), 1)
	if err := os.WriteFile(filepath.Join(repo, "main.go"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	out, se, code = runLintHost(t, repo, role)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, se)
	}
	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 1 || !strings.Contains(out, "lint:errcheck:main.go:Error return value of `os.Setenv`") {
		t.Fatalf("want exactly the new finding, got:\n%s", out)
	}
}
