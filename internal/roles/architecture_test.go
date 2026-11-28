package roles

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"
	"unicode/utf8"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/scanner"
)

func architecturePrompt(t *testing.T) string {
	t.Helper()
	b, err := FS.ReadFile("scanners/architecture/prompt.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEmbeddedArchitectureLoadsAsLLMScanner(t *testing.T) {
	res, err := config.LoadFS(starterFS(t, true), "/repo")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	r, ok := res.Role(config.KindScanner, "architecture")
	if !ok {
		t.Fatalf("no scanner architecture in %+v", res.Roles)
	}
	if r.PromptPath != ".ard/scanners/architecture/prompt.txt" || r.ScanPath != "" || r.Dockerfile != "" || r.Autoclose {
		t.Errorf("unexpected role: %+v", r)
	}
	if r.Every != 72*time.Hour || r.MaxOpen != 3 || r.GroupSize != 1 || r.Timeout != 20*time.Minute || r.MaxBudgetUSD != 2 || r.Model != "claude-sonnet-5-5" {
		t.Errorf("unexpected limits: %+v", r)
	}
	if !r.LLMScanner() || !r.ReadsUntrusted() {
		t.Errorf("LLMScanner=%v ReadsUntrusted=%v", r.LLMScanner(), r.ReadsUntrusted())
	}
}

func TestArchitectureInHostUntrusted(t *testing.T) {
	res, err := config.LoadFS(starterFS(t, true), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.HostUntrusted() {
		if r.Kind == config.KindScanner && r.Name == "architecture" {
			return
		}
	}
	t.Errorf("HostUntrusted = %v, want scanners/architecture", res.HostUntrusted())
}

func TestArchitecturePromptContract(t *testing.T) {
	p := architecturePrompt(t)
	if !utf8.ValidString(p) || len(p) >= 64<<10 {
		t.Fatalf("prompt invalid UTF-8 or too large (%d bytes)", len(p))
	}
	for _, s := range []string{"$ARD_OUT/findings.jsonl", "UNTRUSTED", "<rule>:<path>[:<symbol>]", "$ARD_SIDECAR", "write an empty $ARD_OUT/findings.jsonl and stop",
		"fingerprint", "title", "severity", "group", "files", "body"} {
		if !strings.Contains(p, s) {
			t.Errorf("prompt lacks %q", s)
		}
	}
	for _, s := range []string{"autoclose", "gh "} {
		if strings.Contains(p, s) {
			t.Errorf("prompt contains %q", s)
		}
	}
}

func architectureExamples(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, l := range strings.Split(architecturePrompt(t), "\n") {
		if strings.HasPrefix(l, `{"fingerprint":`) {
			out = append(out, l)
		}
	}
	if len(out) < 2 {
		t.Fatalf("found %d example lines, want at least 2", len(out))
	}
	return out
}

func TestArchitecturePromptExamplesDecode(t *testing.T) {
	fsys := fstest.MapFS{}
	fs, err := scanner.DecodeFindings(strings.NewReader(strings.Join(architectureExamples(t), "\n")))
	if err != nil {
		t.Fatalf("DecodeFindings: %v", err)
	}
	for _, f := range fs {
		for _, p := range f.Files {
			fsys[p] = &fstest.MapFile{}
		}
	}
	exists := func(p string) bool { _, err := fsys.Open(p); return err == nil }
	if errs := scanner.CheckLLMFingerprints(fs, exists); len(errs) > 0 {
		t.Fatalf("CheckLLMFingerprints: %v", errs)
	}
	for _, f := range fs {
		rule, p, _, _ := scanner.ParseLLMFingerprint(f.Fingerprint)
		if n := utf8.RuneCountInString(f.Title); n > 120 {
			t.Errorf("title %q has %d runes", f.Title, n)
		}
		if !strings.HasPrefix(f.Title, p+": ") || !strings.HasSuffix(f.Title, "("+rule+")") {
			t.Errorf("title %q is not <path>: ... (<rule>)", f.Title)
		}
		if f.Severity == scanner.SevCritical || f.Severity == scanner.SevInfo {
			t.Errorf("severity %q", f.Severity)
		}
		if f.Group != "" {
			t.Errorf("group %q", f.Group)
		}
	}
}

func TestArchitectureExampleChecksCatchDrift(t *testing.T) {
	bad := []scanner.Finding{
		{Fingerprint: "adr-1:a/b.go", Files: []string{"c.go"}},
		{Fingerprint: "ADR-1:a/b.go", Files: []string{"a/b.go"}},
		{Fingerprint: "adr-1:a/b.go:x y", Files: []string{"a/b.go"}},
	}
	if errs := scanner.CheckLLMFingerprints(bad, nil); len(errs) != 3 {
		t.Errorf("got %d errors, want 3: %v", len(errs), errs)
	}
}
