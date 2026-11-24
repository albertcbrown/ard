package triage

import (
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/config"
)

// A golden argv for the real Claude adapter, built the way the loop builds it.
func TestCommandClaudeArgv(t *testing.T) {
	h := newHarness(t)
	n := h.human("t", "b", lead)
	cfg := h.cfg
	role, _ := cfg.Role("triage", "lead")
	cmd, err := agentjob.AgentCommand(h.loop.d.Agents, cfg, role, agent.StageTriage, n, 0, []string{".ard-input/1/issue.md"}, executorHandle(), h.loop.d.Model)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{"--append-system-prompt-file=/role/prompt.txt", "--max-budget-usd=5", "--permission-mode=dontAsk"} {
		if !strings.Contains(args, want) {
			t.Errorf("argv lacks %s:\n%s", want, args)
		}
	}
	if strings.Contains(args, tokenVal) {
		t.Error("the credential is on the command line")
	}
	if len(cmd.Env) != 1 || !strings.HasPrefix(cmd.Env[0], "CLAUDE_CODE_OAUTH_TOKEN=") {
		t.Errorf("env = %v", cmd.Env)
	}
	if got := strings.Join(cmd.Fixed, ","); !strings.Contains(got, "CLAUDE_CONFIG_DIR=/home/.claude") {
		t.Errorf("fixed = %s", got)
	}
}

func TestCommandWorkerRolesLine(t *testing.T) {
	h := newHarness(t)
	cfg := h.cfg
	w := func(name string, net bool) config.Role {
		return config.Role{Kind: config.KindWorker, Name: name, Dir: ".ard/workers/" + name, Agent: "claude", Network: net}
	}
	cfg.Roles = append(cfg.Roles, w("docs", false), w("engineer", true))
	const want = "Worker roles: docs (network: false), engineer (network: true)"
	var lines []string
	for _, name := range []string{"lead", "architect"} {
		role, _ := cfg.Role("triage", name)
		cmd, err := agentjob.AgentCommand(h.loop.d.Agents, cfg, role, agent.StageTriage, 1, 0, nil, executorHandle(), h.loop.d.Model)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		for _, a := range cmd.Args {
			for _, l := range strings.Split(a, "\n") {
				if strings.HasPrefix(l, "Worker roles:") {
					got = l
				}
			}
		}
		if got != want {
			t.Errorf("%s: line = %q, want %q", name, got, want)
		}
		lines = append(lines, got)
	}
	if lines[0] != lines[1] {
		t.Error("lines differ between triage roles")
	}
}
