// Package claude adapts the Claude Code CLI (`claude -p`).
package claude

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/albertcbrown/ard/internal/agent"
)

// TestedVersion is the CLI release the flags and stream-json schema were
// verified against. Callers warn when a run reports another version.
const TestedVersion = "2.1.330"

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
	// defaultTools is both --tools and --allowed-tools for every stage (#79).
	defaultTools = "Read,Glob,Grep,Edit,Write,Bash"
	// The deny list is advisory: shell indirection gets around it. The real
	// boundaries are the missing GitHub token, the committer (only
	// .ard/<issue#>/ is committed from triage and review) and the worker
	// diff guard.
	defaultDeny = "Bash(git commit *),Bash(git push *),Bash(git remote *),Bash(gh *)"
	// isolationSettings turns off the git status snapshot (branch and commit
	// subjects are third-party text, invariant 6) and every hook. --restricted
	// still loads --settings, so this is the only settings source besides
	// managed settings (#105).
	isolationSettings = `{"includeGitInstructions":false,"disableAllHooks":true}`
)

// credentialEnv is the env var the CLI reads for each Auth. Exactly one is
// passed: the CLI prefers ANTHROPIC_API_KEY over CLAUDE_CODE_OAUTH_TOKEN, so
// passing both would silently bill the API key.
var credentialEnv = map[agent.Auth]string{
	agent.AuthAPIKey:     "ANTHROPIC_API_KEY",
	agent.AuthOAuthToken: "CLAUDE_CODE_OAUTH_TOKEN",
}

// CredentialEnv returns the env var name the CLI reads for a.
func (*Adapter) CredentialEnv(a agent.Auth) (string, error) {
	n, ok := credentialEnv[a]
	if !ok {
		return "", fmt.Errorf("spec Auth: %q: %w", a, agent.ErrInvalidSpec)
	}
	return n, nil
}

// Command builds the argv and environment. It touches no filesystem.
func (a *Adapter) Command(s agent.Spec) (agent.Command, error) {
	if err := s.Validate(); err != nil {
		return agent.Command{}, err
	}
	tools, allowed, deny := defaultTools, defaultTools, defaultDeny
	if s.Tools != nil {
		// An explicit list replaces the allow list but keeps the deny list.
		allowed = strings.Join(s.Tools, ",")
		tools = toolNames(s.Tools)
	}
	// Planted repo config must not steer the agent. --bare would do that but
	// limits the CLI to Bash, Read and Edit and --tools cannot widen it, so it
	// is not used (#105). Instead: --restricted limits settings to managed
	// settings plus --settings; --safe-mode drops CLAUDE.md/AGENTS.md, skills,
	// agents, hooks, MCP and auto memory; --strict-mcp-config drops .mcp.json;
	// --disable-slash-commands drops skills and commands.
	argv := []string{a.binary, "-p", "--output-format=stream-json", "--verbose",
		"--restricted", "--safe-mode", "--strict-mcp-config", "--no-session-persistence", "--disable-slash-commands",
		"--permission-mode=dontAsk", "--permission-prompts=none", "--settings=" + isolationSettings,
		"--tools=" + tools, "--allowed-tools=" + allowed}
	argv = append(argv, "--disallowed-tools="+deny)
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
	cred, err := a.CredentialEnv(s.Auth)
	if err != nil {
		return agent.Command{}, err
	}
	// CLAUDE_CODE_SUBPROCESS_ENV_SCRUB is deliberately not set. The scrub
	// would keep the model credential out of Bash children, but it needs
	// bubblewrap plus unprivileged user namespaces, which Docker blocks under
	// ard's flags, so every Bash call would fail. The residual risk and the
	// reasoning are in docs/proposal.md (agent CLI adapters).
	return agent.Command{
		Argv: argv,
		Env: map[string]string{
			"CLAUDE_CONFIG_DIR":                        s.ConfigDir,
			"DISABLE_AUTOUPDATER":                      "1",
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
			"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS":     "1",
		},
		PassEnv: []string{cred},
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
