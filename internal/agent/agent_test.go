package agent_test

import (
	"errors"
	"io"
	"testing"

	"github.com/albertcbrown/ard/internal/agent"
)

type fake string

func (f fake) Name() string                             { return string(f) }
func (fake) Command(agent.Spec) (agent.Command, error)  { return agent.Command{}, nil }
func (fake) Parse(io.Reader, int) (agent.Result, error) { return agent.Result{}, nil }

func TestRegistry(t *testing.T) {
	if _, err := agent.NewRegistry(fake("a"), fake("a")); err == nil {
		t.Error("duplicate accepted")
	}
	r, err := agent.NewRegistry(fake("a"), fake("b"))
	if err != nil {
		t.Fatal(err)
	}
	if a, err := r.Get("b"); err != nil || a.Name() != "b" {
		t.Errorf("Get b = %v, %v", a, err)
	}
	if _, err := r.Get("kimi"); !errors.Is(err, agent.ErrUnknownAgent) {
		t.Errorf("err = %v", err)
	}
}

func TestTransient(t *testing.T) {
	for st, want := range map[int]bool{429: true, 500: true, 502: true, 503: true, 529: true, 400: false, 0: false} {
		if got := (agent.Result{APIErrorStatus: st}).Transient(); got != want {
			t.Errorf("%d: %v", st, got)
		}
	}
	if (agent.Result{OK: true, APIErrorStatus: 429}).Transient() {
		t.Error("OK result transient")
	}
}

func TestValidateAuth(t *testing.T) {
	tests := []struct {
		auth agent.Auth
		ok   bool
	}{{"", false}, {"bogus", false}, {agent.AuthAPIKey, true}, {agent.AuthOAuthToken, true}}
	for _, tc := range tests {
		s := agent.Spec{Stage: agent.StageWorker, Role: "default", Issue: 1, PromptFile: "/p", ConfigDir: "/c", MaxBudgetUSD: 1, Auth: tc.auth}
		err := s.Validate()
		if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, agent.ErrInvalidSpec)) {
			t.Errorf("Auth %q: err = %v", tc.auth, err)
		}
	}
}

func TestValidateOutputFile(t *testing.T) {
	scanner := func() agent.Spec {
		return agent.Spec{Stage: agent.StageScanner, Role: "arch", PromptFile: "/p", ConfigDir: "/c",
			OutputFile: "/out/findings.jsonl", MaxBudgetUSD: 1, Auth: agent.AuthAPIKey}
	}
	triage := func() agent.Spec {
		return agent.Spec{Stage: agent.StageTriage, Role: "lead", Issue: 1, PromptFile: "/p", ConfigDir: "/c", MaxBudgetUSD: 1, Auth: agent.AuthAPIKey}
	}
	tests := []struct {
		name string
		spec func() agent.Spec
		mod  func(*agent.Spec)
		ok   bool
	}{
		{"scanner issue 0 abs output", scanner, func(*agent.Spec) {}, true},
		{"scanner issue 1", scanner, func(s *agent.Spec) { s.Issue = 1 }, false},
		{"scanner no output", scanner, func(s *agent.Spec) { s.OutputFile = "" }, false},
		{"scanner relative output", scanner, func(s *agent.Spec) { s.OutputFile = "out/findings.jsonl" }, false},
		{"scanner unclean output", scanner, func(s *agent.Spec) { s.OutputFile = "/out/../x" }, false},
		{"scanner untrusted", scanner, func(s *agent.Spec) { s.Untrusted = []string{"a.md"} }, false},
		{"triage", triage, func(*agent.Spec) {}, true},
		{"triage output", triage, func(s *agent.Spec) { s.OutputFile = "/out/findings.jsonl" }, false},
		{"triage issue 0", triage, func(s *agent.Spec) { s.Issue = 0 }, false},
	}
	for _, tc := range tests {
		s := tc.spec()
		tc.mod(&s)
		err := s.Validate()
		if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, agent.ErrInvalidSpec)) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestRoleFilePath(t *testing.T) {
	tests := []struct {
		issue       int
		stage, role string
		want        string
	}{
		{42, "triage", "lead", ".ard/42/triage-lead.md"},
		{7, "worker", "engineer", ".ard/7/worker-engineer.md"},
		{7, "review", "eng-review", ".ard/7/review-eng-review.md"},
	}
	for _, c := range tests {
		if got := agent.RoleFilePath(c.issue, c.stage, c.role); got != c.want {
			t.Errorf("RoleFilePath(%d, %q, %q) = %q, want %q", c.issue, c.stage, c.role, got, c.want)
		}
	}
}
