// Package claude adapts the Claude Code CLI (`claude -p`).
package claude

import (
	"strconv"
	"strings"

	"github.com/albertcbrown/ard/internal/agent"
)

// TestedVersion is the CLI release the flags and stream-json schema were
// verified against. Callers warn when a run reports another version.
const TestedVersion = "2.1.328"

// Options configures New.
type Options struct{ Binary string } // default "claude"

// Adapter implements agent.Adapter for Claude Code.
type Adapter struct{ binary string }

// New returns an adapter.
func New(o Options) *Adapter {
	if o.Binary == "" {
		o.Binary = "claude"
	}
	return &Adapter{binary: o.Binary}
}

// Name is the value of the config's agent key.
func (*Adapter) Name() string { return "claude" }

const (
	readonlyAllowed = "Read,Glob,Grep,Edit,Write,Bash(git diff *),Bash(git log *),Bash(git show *),Bash(git status *)"
	readonlyTools   = "Read,Glob,Grep,Edit,Write,Bash"
	workerTools     = "Read,Glob,Grep,Edit,Write,Bash"
	// The deny list is advisory; the real boundaries are the missing GitHub
	// token, the diff guard and the committer.
	workerDeny = "Bash(git commit *),Bash(git push *),Bash(git remote *),Bash(gh *)"
)

// Command builds the argv and environment. It touches no filesystem.
func (a *Adapter) Command(s agent.Spec) (agent.Command, error) {
	if err := s.Validate(); err != nil {
		return agent.Command{}, err
	}
	tools, allowed, deny := readonlyTools, readonlyAllowed, ""
	if s.Stage == agent.StageWorker {
		tools, allowed, deny = workerTools, workerTools, workerDeny
	}
	if s.Tools != nil {
		allowed, deny = strings.Join(s.Tools, ","), ""
		tools = toolNames(s.Tools)
	}
	argv := []string{a.binary, "-p", "--output-format=stream-json", "--verbose",
		"--bare", "--restricted", "--strict-mcp-config", "--no-session-persistence", "--disable-slash-commands",
		"--permission-mode=dontAsk", "--permission-prompts=none",
		"--tools=" + tools, "--allowed-tools=" + allowed}
	if deny != "" {
		argv = append(argv, "--disallowed-tools="+deny)
	}
	for _, d := range s.ReadDirs {
		argv = append(argv, "--add-dir="+d)
	}
	if s.Model != "" {
		argv = append(argv, "--model="+s.Model)
	}
	argv = append(argv, "--max-budget-usd="+strconv.FormatFloat(s.MaxBudgetUSD, 'f', -1, 64))
	if s.MaxTurns > 0 {
		argv = append(argv, "--max-turns="+strconv.Itoa(s.MaxTurns))
	}
	argv = append(argv, "--append-system-prompt-file="+s.PromptFile, "--", userMessage(s))
	return agent.Command{
		Argv: argv,
		Env: map[string]string{
			"CLAUDE_CONFIG_DIR":                        s.ConfigDir,
			"DISABLE_AUTOUPDATER":                      "1",
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		},
		PassEnv: []string{"ANTHROPIC_API_KEY"},
	}, nil
}

// toolNames cuts permission rules such as Bash(ls *) down to tool names,
// removing duplicates and keeping order.
func toolNames(rules []string) string {
	var names []string
	seen := map[string]bool{}
	for _, r := range rules {
		n, _, _ := strings.Cut(r, "(")
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	return strings.Join(names, ",")
}
