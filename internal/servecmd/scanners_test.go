package servecmd

import (
	"errors"
	"testing"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/modelauth"
	"github.com/albertcbrown/ard/internal/scanner"
)

func TestScannerEnginesWiring(t *testing.T) {
	llm := config.Role{Kind: config.KindScanner, Name: "arch", PromptPath: ".ard/scanners/arch/prompt.txt"}
	det := config.Role{Kind: config.KindScanner, Name: "cve", ScanPath: ".ard/scanners/cve/scan"}
	cred := modelauth.Credential{Auth: agent.AuthOAuthToken, Name: "CLAUDE_CODE_OAUTH_TOKEN"}
	for _, tc := range []struct {
		name    string
		cred    modelauth.Credential
		mode    string
		wantErr error // Ready of the LLM scanner
		agent   bool  // Agent is an *AgentEngine
	}{
		{"no credential", modelauth.Credential{}, "local", scanner.ErrNoCredential, false},
		{"no credential on k8s", modelauth.Credential{}, "k8s", scanner.ErrNoCredential, false},
		{"credential, local", cred, "local", nil, true},
		{"credential, k8s", cred, "k8s", scanner.ErrExecutorUnsupported, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newTestWiring(t)
			w.Model = tc.cred
			e := scannerEngines(w, scanner.ExecDeps{}, tc.mode)
			if e.Exec == nil {
				t.Fatal("no exec engine")
			}
			if tc.cred.IsZero() && e.Agent != nil {
				t.Errorf("Agent = %#v, want an interface nil (no typed nil)", e.Agent)
			}
			if _, ok := e.Agent.(*scanner.AgentEngine); ok != tc.agent {
				t.Errorf("Agent = %T", e.Agent)
			}
			if err := e.Ready(llm); !errors.Is(err, tc.wantErr) || (tc.wantErr == nil) != (err == nil) {
				t.Errorf("Ready(llm) = %v, want %v", err, tc.wantErr)
			}
			if err := e.Ready(det); err != nil {
				t.Errorf("Ready(cve) = %v", err)
			}
		})
	}
}

func TestRepoWorkDir(t *testing.T) {
	got, err := repoWorkDir("/h", github.Repo{Owner: "Foo", Name: "Bar"})
	if err != nil || got != "/h/.ard/work/foo/bar" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := repoWorkDir("/h", github.Repo{Owner: "..", Name: "x"}); err == nil {
		t.Error("invalid repo accepted")
	}
}

func TestScannerPipelineStatusIssueWired(t *testing.T) {
	w := &wiring{}
	p := scannerPipeline(w, nil, nil)
	if p.StatusIssue == nil {
		t.Fatal("StatusIssue not wired")
	}
	if got := p.StatusIssue(); got != 0 {
		t.Errorf("before startup: %d, want 0", got)
	}
	w.statusIssue.Store(42)
	if got := p.StatusIssue(); got != 42 {
		t.Errorf("after store: %d, want 42", got)
	}
}
