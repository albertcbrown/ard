package triage

import (
	"strings"
	"testing"
)

// A golden argv for the real Claude adapter, built the way the loop builds it.
func TestCommandClaudeArgv(t *testing.T) {
	h := newHarness(t)
	n := h.human("t", "b", lead)
	cfg := h.cfg
	role, _ := cfg.Role("triage", "lead")
	cmd, err := h.loop.command(cfg, role, n, []string{".ard-input/1/issue.md"}, executorHandle())
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
