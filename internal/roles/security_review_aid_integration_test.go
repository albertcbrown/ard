//go:build integration

package roles

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIntegrationSecurityReviewAid builds the security-review image on the
// default ard-agent image (ARD_AGENT_IMAGE, built by `make agent-image`) and
// runs the aid under --network none against fixtures built at run time:
// S1 shell injection, S4 a secret, S5 disabled TLS verification. Needs Docker.
func TestIntegrationSecurityReviewAid(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if _, _, code := dockerOut(t, 30*time.Second, "info"); code != 0 {
		t.Skip("docker daemon not available")
	}
	base := os.Getenv("ARD_AGENT_IMAGE")
	if base == "" {
		t.Skip("ARD_AGENT_IMAGE is not set; run `make agent-image` and set it to the image ID")
	}
	tag := fmt.Sprintf("ard-secaid-it:%d", time.Now().UnixNano())
	if _, se, code := dockerOut(t, 30*time.Minute, "build", "-q", "-t", tag, "--build-arg", "AGENT_IMAGE="+base, "reviewers/security-review"); code != 0 {
		t.Fatalf("docker build: %s", se)
	}
	t.Cleanup(func() { dockerOut(t, time.Minute, "image", "rm", tag) })

	// The token is built from pieces so no complete token-shaped string is in the source.
	token := "ghp_" + strings.Repeat("aB3dE5gH7j", 3) + "xyZ123"
	repo := t.TempDir()
	files := map[string]string{
		"go.mod":                   "module example.com/fixture\n\ngo 1.27\n",
		"s1.go":                    "package main\n\nimport \"os/exec\"\n\nfunc run(name string) error {\n\treturn exec.Command(\"sh\", \"-c\", \"convert \"+name).Run()\n}\n",
		"s4.go":                    "package main\n\nconst apiToken = \"" + token + "\"\n",
		"s5.go":                    "package main\n\nimport \"crypto/tls\"\n\nvar cfg = &tls.Config{InsecureSkipVerify: true}\n",
		"main.go":                  "package main\n\nfunc main() {}\n",
		".ard-input/7/changed.txt": "s1.go\ns4.go\ns5.go\n",
	}
	for p, d := range files {
		full := filepath.Join(repo, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(d), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	role, err := filepath.Abs("reviewers/security-review")
	if err != nil {
		t.Fatal(err)
	}
	args := scanRunArgs(tag, repo, role, "ARD_WORK=/work", "ARD_ISSUE=7", "ARD_ROLE_DIR=/role")
	// Same flags as the executor, plus no network; the command is the aid.
	args = append(args[:2], append([]string{"--network", "none"}, args[2:]...)...)
	args[len(args)-1] = "/role/aid"
	out, se, code := dockerOut(t, 20*time.Minute, args...)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, se)
	}
	found := map[string]bool{}
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		var r struct{ Tool, Rule, File string }
		if err := json.Unmarshal([]byte(ln), &r); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", ln, err)
		}
		found[r.Tool+":"+r.File] = true
	}
	for _, w := range []string{"semgrep:s1.go", "gosec:s1.go", "gitleaks:s4.go", "gosec:s5.go", "semgrep:s5.go"} {
		if !found[w] {
			t.Errorf("no %s result in:\n%s", w, out)
		}
	}
	if strings.Contains(out, token) || strings.Contains(se, token) {
		t.Error("the aid printed the matched secret")
	}
}
