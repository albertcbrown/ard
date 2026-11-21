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
	var hits []finding
	for _, f := range decode(t, out) { // stdlib advisories come and go: ignore them
		if !strings.HasSuffix(f.Fingerprint, ":stdlib") {
			hits = append(hits, f)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("want exactly one non-stdlib finding, got %d:\n%s", len(hits), out)
	}
	if want := "osv:GHSA-69ch-w2m2-3vjp:go.mod:golang.org/x/text"; hits[0].Fingerprint != want {
		t.Errorf("fingerprint = %s, want %s", hits[0].Fingerprint, want)
	}
	if !strings.Contains(hits[0].Body, "Reachable: yes") {
		t.Errorf("want reachable yes:\n%s", hits[0].Body)
	}
}
