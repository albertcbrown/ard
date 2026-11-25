package claude

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestAgentImagePins keeps the default ard-agent image's claude pin in step
// with TestedVersion.
func TestAgentImagePins(t *testing.T) {
	b, err := os.ReadFile("../../../deploy/agent/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	df := string(b)
	arg := func(name string) string {
		m := regexp.MustCompile(`(?m)^ARG ` + name + `=(\S*)\s*$`).FindStringSubmatch(df)
		if m == nil {
			return ""
		}
		return m[1]
	}
	const hint = "a TestedVersion bump must update the Dockerfile's version and checksums in the same PR"
	if got := arg("CLAUDE_VERSION"); got != TestedVersion {
		t.Errorf("deploy/agent/Dockerfile CLAUDE_VERSION = %q, want %q: %s", got, TestedVersion, hint)
	}
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, name := range []string{"CLAUDE_SHA256_AMD64", "CLAUDE_SHA256_ARM64"} {
		if !hex64.MatchString(arg(name)) {
			t.Errorf("deploy/agent/Dockerfile %s = %q, want 64 lowercase hex digits: %s", name, arg(name), hint)
		}
	}
	for n, line := range strings.Split(df, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "#") {
			continue
		}
		low := strings.ToLower(l)
		if regexp.MustCompile(`\bgh\b`).MatchString(low) || strings.Contains(low, "credential.helper") ||
			strings.Contains(low, "credential-helper") || strings.Contains(low, "git-credential") {
			t.Errorf("deploy/agent/Dockerfile line %d installs gh or a credential helper: %q", n+1, l)
		}
	}
}

// TestAgentImageToolPins keeps the ard-agent image's tool pins equal to the
// starter scanners' (#312 gap 7 (a)): a role runs the same tool versions under
// executor k8s (on ard-agent) as locally (on its own image). It also keeps the
// image buildable for both platforms without emulation and free of trufflehog.
func TestAgentImageToolPins(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	arg := func(df, name string) string {
		m := regexp.MustCompile(`(?m)^ARG ` + name + `=(\S*)\s*$`).FindStringSubmatch(df)
		if m == nil {
			return ""
		}
		return m[1]
	}
	agent := read("../../../deploy/agent/Dockerfile")
	scanners := map[string]string{
		"cve":  read("../../roles/scanners/cve/Dockerfile"),
		"lts":  read("../../roles/scanners/lts/Dockerfile"),
		"lint": read("../../roles/scanners/lint/Dockerfile"),
	}
	for _, c := range []struct{ scanner, arg string }{
		{"cve", "GO_IMAGE"}, {"cve", "GOVULNCHECK_VERSION"},
		{"cve", "OSV_SCANNER_VERSION"}, {"cve", "OSV_SCANNER_SHA256_AMD64"}, {"cve", "OSV_SCANNER_SHA256_ARM64"},
		{"cve", "JQ_VERSION"}, {"cve", "JQ_SHA256_AMD64"}, {"cve", "JQ_SHA256_ARM64"},
		{"lts", "YQ_VERSION"}, {"lts", "YQ_SHA256_AMD64"}, {"lts", "YQ_SHA256_ARM64"},
		{"lint", "GOLANGCI_LINT_VERSION"},
	} {
		want := arg(scanners[c.scanner], c.arg)
		if want == "" {
			t.Errorf("scanners/%s/Dockerfile has no ARG %s", c.scanner, c.arg)
			continue
		}
		if got := arg(agent, c.arg); got != want {
			t.Errorf("deploy/agent/Dockerfile %s = %q, want %q (scanners/%s/Dockerfile): keep them in step", c.arg, got, want, c.scanner)
		}
	}
	if !regexp.MustCompile(`@sha256:[0-9a-f]{64}$`).MatchString(arg(agent, "GO_IMAGE")) {
		t.Errorf("deploy/agent/Dockerfile GO_IMAGE = %q, want a digest pin", arg(agent, "GO_IMAGE"))
	}
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, name := range []string{"SHELLCHECK_SHA256_AMD64", "SHELLCHECK_SHA256_ARM64"} {
		if !hex64.MatchString(arg(agent, name)) {
			t.Errorf("deploy/agent/Dockerfile %s = %q, want 64 lowercase hex digits", name, arg(agent, name))
		}
	}
	stage := ""
	for n, line := range strings.Split(agent, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "#") {
			continue
		}
		if strings.HasPrefix(l, "FROM ") {
			stage = l
			if !strings.Contains(l, "${GO_IMAGE}") {
				t.Errorf("deploy/agent/Dockerfile line %d: every stage must start FROM the pinned ${GO_IMAGE}: %q", n+1, l)
			}
		}
		if strings.Contains(strings.ToLower(l), "trufflehog") {
			t.Errorf("deploy/agent/Dockerfile line %d installs trufflehog (AGPL, #217): %q", n+1, l)
		}
		if strings.HasPrefix(l, "RUN ") && !strings.Contains(stage, "--platform=$BUILDPLATFORM") {
			t.Errorf("deploy/agent/Dockerfile line %d: a RUN outside a $BUILDPLATFORM stage needs emulation for the other platform", n+1)
		}
	}
}
