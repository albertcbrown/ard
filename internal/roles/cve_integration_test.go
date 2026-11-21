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

// Needs the real osv-scanner, govulncheck, go, jq and network access. The
// vulnerable module is written into a temp dir and never committed: ard's own
// cve scanner would otherwise report it.
func TestIntegrationRealTools(t *testing.T) {
	for _, n := range []string{"osv-scanner", "govulncheck", "go", "jq"} {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s not on PATH", n)
		}
	}
	dir := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// golang.org/x/text v0.3.7 -> GO-2022-1059 / GHSA-69ch-w2m2-3vjp, reached
	// through language.ParseAcceptLanguage. Re-confirm against osv.dev if the
	// advisory is ever withdrawn.
	write("go.mod", "module example.com/vulnfixture\n\ngo 1.21\n\nrequire golang.org/x/text v0.3.7\n")
	write("main.go", `package main

import "golang.org/x/text/language"

func main() { _, _, _ = language.ParseAcceptLanguage("en") }
`)
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	scan, err := filepath.Abs("scanners/cve/scan")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(scan)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("scan: %v\n%s", err, stderr.String())
	}
	const want = "osv:GHSA-69ch-w2m2-3vjp:go.mod:golang.org/x/text"
	hits := map[string]finding{}
	for _, f := range decode(t, out) { // stdlib advisories come and go: ignore them
		if !strings.HasSuffix(f.Fingerprint, ":stdlib") {
			hits[f.Fingerprint] = f
		}
	}
	// x/text v0.3.7 collects advisories over time: require the called one, and
	// that anything else on x/text is module-level only.
	g, ok := hits[want]
	if !ok {
		t.Fatalf("missing %s:\n%s", want, out)
	}
	if g.Severity != "high" || !strings.Contains(g.Body, "Reachable: yes") ||
		!strings.Contains(g.Body, "Reported by: govulncheck, osv-scanner") {
		t.Errorf("unexpected finding:\n%+v", g)
	}
	for fp, f := range hits {
		if !strings.HasSuffix(fp, ":go.mod:golang.org/x/text") {
			t.Errorf("finding on an unexpected module: %s", fp)
		}
		if fp != want && !strings.Contains(f.Body, "Reachable: no") {
			t.Errorf("%s should be unreachable:\n%s", fp, f.Body)
		}
	}
}

// ard's own checkout, exported from git so untracked worktrees and modules are
// not scanned. Logs the finding count; a new advisory must not fail the test.
func TestIntegrationScansRepoClean(t *testing.T) {
	for _, n := range []string{"osv-scanner", "govulncheck", "go", "jq", "git", "tar"} {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s not on PATH", n)
		}
	}
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skipf("not in a git checkout: %v", err)
	}
	dir := t.TempDir()
	arch := exec.Command("sh", "-c", `git -C "$1" archive HEAD | tar -x -C "$2"`, "sh", strings.TrimSpace(string(top)), dir)
	if out, err := arch.CombinedOutput(); err != nil {
		t.Fatalf("export: %v\n%s", err, out)
	}
	scan, err := filepath.Abs("scanners/cve/scan")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(scan)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("scan: %v\n%s", err, stderr.String())
	}
	t.Logf("%d finding(s)\n%s", len(decode(t, out)), stderr.String())
}
