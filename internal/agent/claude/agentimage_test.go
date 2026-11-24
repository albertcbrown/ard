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
