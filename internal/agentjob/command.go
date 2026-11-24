package agentjob

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/modelauth"
)

// Command maps an agent invocation to an executor command. It is pure. The
// adapter must ask for exactly the credential the controller holds.
func Command(ac agent.Command, cred modelauth.Credential) (executor.Command, error) {
	if len(ac.Argv) == 0 {
		return executor.Command{}, fmt.Errorf("agent command has no argv: %w", agent.ErrInvalidSpec)
	}
	if len(ac.PassEnv) != 1 || cred.IsZero() || ac.PassEnv[0] != cred.Name {
		return executor.Command{}, fmt.Errorf("agent wants a credential other than the one set: %w", agent.ErrInvalidSpec)
	}
	fixed := make([]string, 0, len(ac.Env))
	for k, v := range ac.Env {
		fixed = append(fixed, k+"="+v)
	}
	sort.Strings(fixed)
	return executor.Command{Path: ac.Argv[0], Args: ac.Argv[1:], Fixed: fixed, Env: cred.Env()}, nil
}

// AgentCommand builds the agent invocation for a prepared job of the given
// stage (an agent.Stage* word): the role's adapter, the prompt at
// h.View.Role+"/prompt.txt", the untrusted paths, the per-job config dir, the
// model and the budget (the role's, else cfg's, unless budget is positive: the
// worker passes what is left of its job budget). The adapter never reads the
// prompt or the untrusted files; they are paths only.
func AgentCommand(reg *agent.Registry, cfg *config.Resolved, role config.Role, stage string, n int, budget float64, untrusted []string, h executor.Handle, cred modelauth.Credential) (executor.Command, error) {
	a, err := reg.Get(role.Agent)
	if err != nil {
		return executor.Command{}, err
	}
	if budget <= 0 {
		if budget = role.MaxBudgetUSD; budget == 0 {
			budget = cfg.MaxBudgetUSD
		}
	}
	spec := agent.Spec{
		Stage: stage, Role: role.Name, Issue: n,
		PromptFile: h.View.Role + "/prompt.txt", Untrusted: untrusted,
		ConfigDir: h.View.ConfigDir, Model: role.Model, MaxBudgetUSD: budget, Auth: cred.Auth, Network: role.Network,
	}
	if h.View.Sidecar != "" {
		spec.ReadDirs = []string{h.View.Sidecar}
	}
	ac, err := a.Command(spec)
	if err != nil {
		return executor.Command{}, err
	}
	return Command(ac, cred)
}

// ExecJob fills the role-derived fields of an executor job: RoleDir,
// Dockerfile and Sidecar from cfg and Timeout from the role.
func ExecJob(cfg *config.Resolved, role config.Role, j executor.Job) executor.Job {
	j.RoleDir = filepath.Join(cfg.Repo, filepath.FromSlash(role.Dir))
	j.Timeout = role.Timeout
	if role.Dockerfile != "" {
		j.Dockerfile = filepath.Join(cfg.Repo, filepath.FromSlash(role.Dockerfile))
	}
	if sc, ok := cfg.SidecarFor(role); ok {
		j.Sidecar = executor.Sidecar{Name: role.Sidecar, Repo: sc.Repo, Ref: sc.Ref}
	}
	if !role.Network {
		j.Network = executor.NetworkAdvisory
	}
	return j
}
