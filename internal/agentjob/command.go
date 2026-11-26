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
	spec := agent.Spec{
		Stage: stage, Role: role.Name, Issue: n,
		PromptFile: h.View.Role + "/prompt.txt", Untrusted: untrusted,
		ConfigDir: h.View.ConfigDir, Model: role.Model, MaxBudgetUSD: jobBudget(cfg, role, budget), Auth: cred.Auth, Network: role.Network,
	}
	if stage == agent.StageTriage {
		for _, w := range cfg.RolesOf(config.KindWorker) {
			spec.WorkerRoles = append(spec.WorkerRoles, agent.WorkerNetwork{Role: w.Name, Network: w.Network})
		}
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

// ScannerFindingsFile is the file, inside the job's output dir, an LLM
// scanner writes its findings to.
const ScannerFindingsFile = "findings.jsonl"

// ScannerCommand builds the agent invocation for an LLM scanner run: the
// role's adapter, h.View.Role+"/prompt.txt", Stage scanner, Issue 0, the
// output file h.View.Out+"/findings.jsonl", the sidecar as a read dir, the
// per-job config dir, the role's model and its max_budget_usd (else cfg's).
// It fails (wrapping agent.ErrInvalidSpec) when h.View.Out is "": the job
// was prepared without Output.
func ScannerCommand(reg *agent.Registry, cfg *config.Resolved, role config.Role, h executor.Handle, cred modelauth.Credential) (executor.Command, error) {
	if h.View.Out == "" {
		return executor.Command{}, fmt.Errorf("scanner %s: job has no output dir: %w", role.Name, agent.ErrInvalidSpec)
	}
	a, err := reg.Get(role.Agent)
	if err != nil {
		return executor.Command{}, err
	}
	spec := agent.Spec{
		Stage: agent.StageScanner, Role: role.Name,
		PromptFile: h.View.Role + "/prompt.txt", OutputFile: h.View.Out + "/" + ScannerFindingsFile,
		ConfigDir: h.View.ConfigDir, Model: role.Model, MaxBudgetUSD: jobBudget(cfg, role, 0), Auth: cred.Auth, Network: role.Network,
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

// jobBudget is budget when positive, else the role's max_budget_usd, else
// cfg's.
func jobBudget(cfg *config.Resolved, role config.Role, budget float64) float64 {
	if budget > 0 {
		return budget
	}
	if role.MaxBudgetUSD != 0 {
		return role.MaxBudgetUSD
	}
	return cfg.MaxBudgetUSD
}

// ExecJob fills the role-derived fields of an executor job: RoleDir,
// Dockerfile and Sidecar from cfg and Timeout from the role.
func ExecJob(cfg *config.Resolved, role config.Role, j executor.Job) executor.Job {
	j.RoleDir = filepath.Join(cfg.Repo, filepath.FromSlash(role.Dir))
	j.RoleSource = executor.RoleSource{SHA: cfg.SHA, Path: role.Dir}
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
