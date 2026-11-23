package scanner

import (
	"strings"
	"testing"
)

func TestParseLLMFingerprint(t *testing.T) {
	long := "r:" + strings.Repeat("a", 600)
	good := []string{"adr-0007:internal/foo/bar.go:Server.Start", "invariant-3:internal/committer", "r:a.go"}
	for _, fp := range good {
		if _, _, _, ok := ParseLLMFingerprint(fp); !ok {
			t.Errorf("%q rejected", fp)
		}
	}
	bad := []string{"a b:x.go", "Rule:x.go", "r:../x", "r:/x", "r:a/../b", "r:.", "r:a//b", "r:x.go:1abc", "r::x", "r:x.go:", ":x.go",
		"r:x.go:a:b", "r:x y.go", "r", long, ""}
	for _, fp := range bad {
		if _, _, _, ok := ParseLLMFingerprint(fp); ok {
			t.Errorf("%q accepted", fp)
		}
	}
	r, p, s, _ := ParseLLMFingerprint("adr-1:a/b.go:T.m")
	if r != "adr-1" || p != "a/b.go" || s != "T.m" {
		t.Errorf("parts = %q %q %q", r, p, s)
	}
}

func TestCheckLLMFingerprints(t *testing.T) {
	secret := "Secret Fingerprint"
	fs := []Finding{
		{Fingerprint: "r:a.go", Files: []string{"a.go"}},
		{Fingerprint: secret, Files: []string{"a.go"}},
		{Fingerprint: "r:b.go", Files: []string{"a.go"}},
		{Fingerprint: "r:gone.go", Files: []string{"gone.go"}},
	}
	errs := CheckLLMFingerprints(fs, func(p string) bool { return p != "gone.go" })
	if len(errs) != 3 || errs[0].Line != 2 || errs[1].Line != 3 || errs[2].Line != 4 {
		t.Fatalf("errs = %v", errs)
	}
	for _, e := range errs {
		if strings.Contains(e.Msg, "Secret") || strings.Contains(e.Msg, ".go") {
			t.Errorf("msg leaks content: %q", e.Msg)
		}
	}
	if len(CheckLLMFingerprints(fs[:1], nil)) != 0 {
		t.Error("nil exists must treat paths as present")
	}
	many := make([]Finding, 30)
	if n := len(CheckLLMFingerprints(many, nil)); n != 20 {
		t.Errorf("errors = %d, want 20", n)
	}
	if got := LLMGrammarLines(DecodeErrors{{Line: 3}, {Line: 7}}); got != "invalid findings at lines 3, 7" {
		t.Errorf("got %q", got)
	}
	if got := LLMGrammarLines(CheckLLMFingerprints(many, nil)); got != "invalid findings at lines 1, 2, 3, 4, 5 (and 15 more)" {
		t.Errorf("got %q", got)
	}
}
