package agentjob

import (
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
