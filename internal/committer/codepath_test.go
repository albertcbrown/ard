package committer

import "testing"

func TestCheckCodePath(t *testing.T) {
	refused := []string{
		".github/workflows/ci.yml", ".github/CODEOWNERS", ".GitHub/x",
		".ard/config.yaml", ".ard/workers/engineer/prompt.txt", ".ard/12/worker-engineer.md", ".ARD/Config.yaml",
		".ard-input/12/issue.md", "a/.git/hooks/x", "a/.GIT/x", ".gitmodules", "sub/.gitmodules",
		"./a", "a//b", "/abs", "a\\b", "a\x01b", "", ".github", ".ard",
		// secret-shaped
		".env", "config/.env.prod", "certs/server.pem", "id_ed25519.pub", ".aws/credentials",
		"x/.docker/config.json", "service-account-prod.json", "a/.NPMRC", "k.p12",
	}
	for _, p := range refused {
		if err := CheckCodePath(12, p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
	allowed := []string{
		".env.example", "src/env.go", "docs/github/x.md", "docs/.github/x",
		"internal/redact/rules.go", "keys.go", "service-account.go", "a.txt", "dir/.gitignore",
	}
	for _, p := range allowed {
		if err := CheckCodePath(12, p); err != nil {
			t.Errorf("%q refused: %v", p, err)
		}
	}
}
