package config

import (
	"strings"
	"testing"
)

func TestNormaliseProtectedPath(t *testing.T) {
	tests := []struct {
		in, want, errSub string
	}{
		{in: ".github/", want: ".github/"},
		{in: ".github//", want: ".github/"},
		{in: "./.github/", want: ".github/"},
		{in: "infra/./", want: "infra/"},
		{in: "a/b/../c/", want: "a/c/"},
		{in: "infra", want: "infra"},
		{in: "**/*.tf", want: "**/*.tf"},
		{in: "docs/*.md", want: "docs/*.md"},
		{in: "", errSub: "must not be empty"},
		{in: " .github/", errSub: "whitespace"},
		{in: ".github/ ", errSub: "whitespace"},
		{in: " ", errSub: "whitespace"},
		{in: "\t", errSub: "whitespace"},
		{in: "/etc/", errSub: "must be relative"},
		{in: "../x/", errSub: "escapes the repo"},
		{in: "a/../../x", errSub: "escapes the repo"},
		{in: "./", errSub: "names the whole repo"},
		{in: ".", errSub: "names the whole repo"},
		{in: "docs/*/", errSub: "contains glob characters"},
		{in: "src/[a", errSub: "invalid glob"},
		{in: "{a,b", errSub: "invalid glob"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := normaliseProtectedPath(tt.in)
			if tt.errSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("err = %v, want containing %q", err, tt.errSub)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestProtected(t *testing.T) {
	r := &Resolved{ProtectedPaths: []string{
		".github/", ".ard/config.yaml", ".ard/scanners/", ".ard/triage/", ".ard/workers/", ".ard/reviewers/",
		"CODEOWNERS", "infra/", "**/*.tf", "docs/*.md",
	}}
	tests := []struct {
		name, path, pattern string
		ok                  bool
	}{
		{"prefix file", ".github/workflows/ci.yml", ".github/", true},
		{"prefix bare name", ".github", ".github/", true},
		{"role dir", ".ard/scanners/cve/prompt.txt", ".ard/scanners/", true},
		{"near miss suffix", ".githubx", "", false},
		{"near miss suffix dir", ".githubx/ci.yml", "", false},
		{"near miss dash", ".github-old/x", "", false},
		{"nested not anchored", "x/.github/ci.yml", "", false},
		{"config bak", ".ard/config.yaml.bak", "", false},
		{"issue dir", ".ard/42/plan.md", "", false},
		{"infrastructure", "infrastructure/main.go", "", false},
		{"glob exact", ".ard/config.yaml", ".ard/config.yaml", true},
		{"codeowners root", "CODEOWNERS", "CODEOWNERS", true},
		{"codeowners nested", "docs/CODEOWNERS", "", false},
		{"doublestar nested", "modules/vpc/main.tf", "**/*.tf", true},
		{"doublestar root", "main.tf", "**/*.tf", true},
		{"star one segment", "docs/a.md", "docs/*.md", true},
		{"star not deeper", "docs/sub/a.md", "", false},
		{"case dir", ".GitHub/workflows/ci.yml", "", false},
		{"case file", "codeowners", "", false},
		{"dot prefix fails closed", "./.github/x", "", true},
		{"dotdot fails closed", "a/../.github/x", "", true},
		{"dotdot into protected", ".github/../src/x", "", true},
		{"trailing slash", "src/", "", true},
		{"double slash", "src//x", "", true},
		{"backslash", ".github\\workflows\\ci.yml", "", true},
		{"control char", "a\x00b", "", true},
		{"newline", "src/x\n", "", true},
		{"empty fails closed", "", "", true},
		{"absolute fails closed", "/etc/passwd", "", true},
		{"escape fails closed", "../.github/x", "", true},
		{"dotdot fails closed", "..", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pat, ok := r.ProtectedBy(tt.path)
			if pat != tt.pattern || ok != tt.ok {
				t.Errorf("ProtectedBy(%q) = %q, %v; want %q, %v", tt.path, pat, ok, tt.pattern, tt.ok)
			}
			if r.Protected(tt.path) != tt.ok {
				t.Errorf("Protected(%q) disagrees with ProtectedBy", tt.path)
			}
		})
	}

	t.Run("first match wins", func(t *testing.T) {
		r := &Resolved{ProtectedPaths: []string{"infra/", "**/*.tf"}}
		if pat, ok := r.ProtectedBy("infra/main.tf"); !ok || pat != "infra/" {
			t.Errorf("got %q, %v", pat, ok)
		}
	})
	t.Run("empty list", func(t *testing.T) {
		r := &Resolved{}
		if r.Protected(".github/x") {
			t.Error("empty list protected a path")
		}
		if !r.Protected("") {
			t.Error("malformed path must stay protected")
		}
	})
}
