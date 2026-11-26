package agentjob

import (
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/modelauth"
)

type specSpy struct{ got agent.Spec }

func (s *specSpy) Name() string { return "spy" }
func (s *specSpy) Command(sp agent.Spec) (agent.Command, error) {
	s.got = sp
	return agent.Command{Argv: []string{"x"}, PassEnv: []string{"K"}}, nil
}
func (s *specSpy) Parse(io.Reader, int) (agent.Result, error) { return agent.Result{}, nil }

func TestScannerCommand(t *testing.T) {
	spy := &specSpy{}
	reg, err := agent.NewRegistry(spy)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Resolved{MaxBudgetUSD: 5}
	cred := modelauth.Credential{Name: "K", Auth: agent.AuthOAuthToken}
	view := executor.Paths{Work: "/work", Role: "/role", ConfigDir: "/ard-config", Sidecar: "/sidecar", Out: "/out"}
	base := agent.Spec{Stage: agent.StageScanner, Role: "arch", PromptFile: "/role/prompt.txt", OutputFile: "/out/findings.jsonl",
		ReadDirs: []string{"/sidecar"}, ConfigDir: "/ard-config", Model: "claude-opus-4-5", MaxBudgetUSD: 2, Auth: agent.AuthOAuthToken}
	tests := []struct {
		name    string
		role    func(*config.Role)
		view    func(*executor.Paths)
		want    func(*agent.Spec)
		wantErr bool
	}{
		{"role budget wins", func(*config.Role) {}, func(*executor.Paths) {}, func(*agent.Spec) {}, false},
		{"global budget", func(r *config.Role) { r.MaxBudgetUSD = 0 }, func(*executor.Paths) {}, func(s *agent.Spec) { s.MaxBudgetUSD = 5 }, false},
		{"no sidecar", func(*config.Role) {}, func(v *executor.Paths) { v.Sidecar = "" }, func(s *agent.Spec) { s.ReadDirs = nil }, false},
		{"network", func(r *config.Role) { r.Network = true }, func(*executor.Paths) {}, func(s *agent.Spec) { s.Network = true }, false},
		{"no output dir", func(*config.Role) {}, func(v *executor.Paths) { v.Out = "" }, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spy.got = agent.Spec{}
			role := config.Role{Kind: config.KindScanner, Name: "arch", Agent: "spy", Model: "claude-opus-4-5", MaxBudgetUSD: 2}
			tc.role(&role)
			h := executor.Handle{View: view}
			tc.view(&h.View)
			c, err := ScannerCommand(reg, cfg, role, h, cred)
			if tc.wantErr {
				if !errors.Is(err, agent.ErrInvalidSpec) || spy.got.Role != "" {
					t.Fatalf("err = %v, adapter called with %+v", err, spy.got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := base
			want.ReadDirs = append([]string(nil), base.ReadDirs...)
			tc.want(&want)
			if !reflect.DeepEqual(spy.got, want) {
				t.Errorf("spec:\n got %+v\nwant %+v", spy.got, want)
			}
			if c.Path != "x" || !reflect.DeepEqual(c.Env, cred.Env()) {
				t.Errorf("command %+v", c)
			}
		})
	}
}

func TestJobBudget(t *testing.T) {
	cfg := &config.Resolved{MaxBudgetUSD: 5}
	tests := []struct{ role, budget, want float64 }{
		{0, 0, 5}, {2, 0, 2}, {2, 0.5, 0.5}, {0, 0.5, 0.5},
	}
	for _, tc := range tests {
		if got := jobBudget(cfg, config.Role{MaxBudgetUSD: tc.role}, tc.budget); got != tc.want {
			t.Errorf("role %v budget %v: got %v, want %v", tc.role, tc.budget, got, tc.want)
		}
	}
}

func TestAgentCommandWorkerRoles(t *testing.T) {
	spy := &specSpy{}
	reg, err := agent.NewRegistry(spy)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(k config.Kind, name string, net bool) config.Role {
		return config.Role{Kind: k, Name: name, Agent: "spy", Network: net}
	}
	cfg := &config.Resolved{MaxBudgetUSD: 1, Roles: []config.Role{
		mk(config.KindTriage, "lead", true), mk(config.KindWorker, "docs", false), mk(config.KindWorker, "engineer", true),
	}}
	cred := modelauth.Credential{Name: "K", Auth: agent.AuthAPIKey}
	want := []agent.WorkerNetwork{{Role: "docs", Network: false}, {Role: "engineer", Network: true}}
	for stage, exp := range map[string][]agent.WorkerNetwork{
		agent.StageTriage: want, agent.StageWorker: nil, agent.StageReview: nil,
	} {
		spy.got = agent.Spec{}
		if _, err := AgentCommand(reg, cfg, cfg.Roles[0], stage, 1, 0, nil, executor.Handle{}, cred); err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		if !reflect.DeepEqual(spy.got.WorkerRoles, exp) {
			t.Errorf("%s: WorkerRoles = %v, want %v", stage, spy.got.WorkerRoles, exp)
		}
	}
	// No worker roles: no entries, no error.
	cfg.Roles = cfg.Roles[:1]
	if _, err := AgentCommand(reg, cfg, cfg.Roles[0], agent.StageTriage, 1, 0, nil, executor.Handle{}, cred); err != nil || len(spy.got.WorkerRoles) != 0 {
		t.Errorf("err=%v roles=%v", err, spy.got.WorkerRoles)
	}
}
