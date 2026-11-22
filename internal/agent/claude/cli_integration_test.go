//go:build integration

// These tests run the real CLI and make no paid model call. One has no API
// key; the others send a dummy key to a loopback fake that answers 400. Re-run
// them on every TestedVersion bump: part of the isolation they pin (AGENTS.md,
// and CLAUDE.md, agents and skills under --restricted) is behaviour observed
// only on the tested release. Settings env isolation is not pinned: no Bash
// call is scripted.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// plantRepo builds a git worktree full of planted agent config, with a
// CLAUDE.md in its parent directory too. markers is a separate directory the
// planted hooks and MCP server touch files in.
func plantRepo(t *testing.T) (work, markers string) {
	t.Helper()
	root, markers := t.TempDir(), t.TempDir()
	work = filepath.Join(root, "work")
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "PLANTED_PARENT\n")
	writeFile(t, filepath.Join(work, "CLAUDE.md"), "PLANTED_CLAUDE_MD\n")
	writeFile(t, filepath.Join(work, ".claude/rules/r.md"), "PLANTED_RULES\n")
	writeFile(t, filepath.Join(work, "sub/CLAUDE.md"), "PLANTED_NESTED\n")
	writeFile(t, filepath.Join(work, "sub/f.txt"), "f\n")
	hook := func(ev string) map[string]any {
		return map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "touch " + filepath.Join(markers, "hook-"+ev)}}}
	}
	settings, err := json.Marshal(map[string]any{
		"env":         map[string]string{"PLANTED_ENV": "1"},
		"outputStyle": "planted-style",
		"hooks": map[string]any{
			"SessionStart":     []any{hook("SessionStart")},
			"UserPromptSubmit": []any{hook("UserPromptSubmit")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, ".claude/settings.json"), string(settings))
	writeFile(t, filepath.Join(work, ".claude/output-styles/planted-style.md"), "---\nname: planted-style\ndescription: planted\n---\nPLANTED_STYLE\n")
	writeFile(t, filepath.Join(work, ".claude/agents/planted-agent.md"), "---\nname: planted-agent\ndescription: planted\n---\nPLANTED_AGENT\n")
	writeFile(t, filepath.Join(work, ".claude/skills/planted-skill/SKILL.md"), "---\nname: planted-skill\ndescription: planted\n---\nPLANTED_SKILL\n")
	mcp, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"planted": map[string]any{
		"command": "/bin/sh", "args": []string{"-c", "touch " + filepath.Join(markers, "mcp")}}}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, ".mcp.json"), string(mcp))
	gittest.Run(t, work, "init", "--initial-branch=main")
	gittest.Run(t, work, "add", "-A")
	gittest.Run(t, work, "commit", "-m", "PLANTED_COMMIT")
	return work, markers
}

// plantAgentsRepo builds a repo whose only planted config is an AGENTS.md (the
// CLI reads it only when there is no CLAUDE.md).
func plantAgentsRepo(t *testing.T) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "work")
	writeFile(t, filepath.Join(work, "AGENTS.md"), "PLANTED_AGENTS_MD\n")
	gittest.Run(t, work, "init", "--initial-branch=main")
	gittest.Run(t, work, "add", "-A")
	gittest.Run(t, work, "commit", "-m", "PLANTED_COMMIT")
	return work
}

// workerArgv returns the worker command for the planted repo.
func workerArgv(t *testing.T, work string) agent.Command {
	t.Helper()
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not on PATH")
	}
	prompt := filepath.Join(t.TempDir(), "prompt.txt")
	writeFile(t, prompt, "test")
	c, err := New(Options{Binary: bin}).Command(agent.Spec{Stage: agent.StageWorker, Role: "default", Issue: 1,
		PromptFile: prompt, ConfigDir: t.TempDir(), MaxBudgetUSD: 0.01})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// runCLI runs argv in work with a from-scratch environment and returns stdout
// and the exit code. HOME is its own empty directory, never the worktree.
func runCLI(t *testing.T, work string, c agent.Command, argv []string, extraEnv ...string) ([]byte, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = work
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	for k, v := range c.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Env = append(cmd.Env, extraEnv...)
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run()
	return out.Bytes(), cmd.ProcessState.ExitCode()
}

type cliInit struct {
	Tools          []string          `json:"tools"`
	MCPServers     []any             `json:"mcp_servers"`
	PermissionMode string            `json:"permissionMode"`
	Plugins        []json.RawMessage `json:"plugins"`
	Agents         []string          `json:"agents"`
	Skills         []string          `json:"skills"`
	SlashCommands  []string          `json:"slash_commands"`
	OutputStyle    string            `json:"output_style"`
	APIKeySource   string            `json:"apiKeySource"`
}

func cliInitEvent(t *testing.T, out []byte) cliInit {
	t.Helper()
	for _, l := range bytes.Split(out, []byte("\n")) {
		var h struct{ Type, Subtype string }
		if json.Unmarshal(l, &h) == nil && h.Type == "system" && h.Subtype == "init" {
			var in cliInit
			if err := json.Unmarshal(l, &in); err != nil {
				t.Fatal(err)
			}
			return in
		}
	}
	t.Fatalf("no system/init event in %s", out)
	return cliInit{}
}

// fakeAPI answers every request with 400 (a 401 makes the CLI retry for
// minutes) and records the bodies.
func fakeAPI(t *testing.T) (url string, bodies func() [][]byte) {
	t.Helper()
	var mu sync.Mutex
	var got [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, b)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"fake"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() [][]byte {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

func markerNames(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var n []string
	for _, e := range es {
		n = append(n, e.Name())
	}
	return n
}

// checkIsolated asserts the init event shows no planted config.
func checkIsolated(t *testing.T, in cliInit, markers string) {
	t.Helper()
	want := []string{"Bash", "Edit", "Glob", "Grep", "Read", "Write"}
	got := slices.Clone(in.Tools)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("tools = %v, want %v", got, want)
	}
	if len(in.MCPServers) != 0 || in.PermissionMode != "dontAsk" {
		t.Errorf("mcp_servers %v, permissionMode %q", in.MCPServers, in.PermissionMode)
	}
	if slices.Contains(in.Agents, "planted-agent") {
		t.Errorf("planted agent loaded: %v", in.Agents)
	}
	if slices.Contains(in.Skills, "planted-skill") || slices.Contains(in.SlashCommands, "planted-skill") {
		t.Errorf("planted skill loaded: %v %v", in.Skills, in.SlashCommands)
	}
	if in.OutputStyle != "default" {
		t.Errorf("output_style = %q", in.OutputStyle)
	}
	for _, p := range in.Plugins {
		if !bytes.Contains(p, []byte("@builtin")) {
			t.Errorf("non-builtin plugin %s", p)
		}
	}
	if m := markerNames(t, markers); len(m) != 0 {
		t.Errorf("planted hook or MCP server ran: %v", m)
	}
}

// Runs the real CLI with no API key, which forces an auth failure with no
// spend, and checks that it accepts every flag and loads no planted config.
func TestCLIAcceptsFlags(t *testing.T) {
	work, markers := plantRepo(t)
	c := workerArgv(t, work)
	// The tool set the adapter asked for, as a sorted set.
	tv, _ := flag(c.Argv, "--tools")
	asked := strings.Split(tv, ",")
	slices.Sort(asked)

	url, bodies := fakeAPI(t)
	out, code := runCLI(t, work, c, c.Argv, "ANTHROPIC_BASE_URL="+url)
	if n := len(bodies()); n != 0 {
		t.Errorf("%d requests reached the fake API with no key; want none", n)
	}
	in := cliInitEvent(t, out)
	got := slices.Clone(in.Tools)
	slices.Sort(got)
	if !slices.Equal(got, asked) {
		t.Errorf("init tools %v, adapter asked for %v", got, asked)
	}
	checkIsolated(t, in, markers)
	if in.APIKeySource != "none" {
		t.Errorf("apiKeySource = %q, want none", in.APIKeySource)
	}
	res, err := New(Options{}).Parse(bytes.NewReader(out), code)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !res.IsError || res.TerminalReason != "api_error" {
		t.Errorf("unexpected %+v", res)
	}
}

// Same argv against a loopback fake API: no request body may carry planted
// text, the git status snapshot or the background-task tool parameter.
func TestCLIIgnoresPlantedConfig(t *testing.T) {
	work, markers := plantRepo(t)
	c := workerArgv(t, work)
	url, bodies := fakeAPI(t)
	out, _ := runCLI(t, work, c, c.Argv, "ANTHROPIC_API_KEY=dummy-not-a-key", "ANTHROPIC_BASE_URL="+url)
	checkIsolated(t, cliInitEvent(t, out), markers)
	assertNoneContain(t, bodies(), "PLANTED_", "gitStatus", "run_in_background")

	awork := plantAgentsRepo(t)
	url, bodies = fakeAPI(t)
	runCLI(t, awork, c, c.Argv, "ANTHROPIC_API_KEY=dummy-not-a-key", "ANTHROPIC_BASE_URL="+url)
	assertNoneContain(t, bodies(), "PLANTED_")
}

func assertNoneContain(t *testing.T, bs [][]byte, bad ...string) {
	t.Helper()
	if len(bs) == 0 {
		t.Fatal("no request reached the fake API")
	}
	for _, b := range bs {
		for _, x := range bad {
			if bytes.Contains(b, []byte(x)) {
				t.Errorf("request body contains %q", x)
			}
		}
	}
}

func anyContains(bs [][]byte, s string) bool {
	for _, b := range bs {
		if bytes.Contains(b, []byte(s)) {
			return true
		}
	}
	return false
}

// Positive control: without the isolation flags the same repo does leak, so
// the tests above cannot pass vacuously after a CLI release moves discovery.
func TestCLIPlantedConfigControl(t *testing.T) {
	work, markers := plantRepo(t)
	c := workerArgv(t, work)
	drop := map[string]bool{"--restricted": true, "--safe-mode": true, "--strict-mcp-config": true, "--disable-slash-commands": true}
	var argv []string
	for _, a := range c.Argv {
		if drop[a] || strings.HasPrefix(a, "--settings=") {
			continue
		}
		argv = append(argv, a)
	}
	url, bodies := fakeAPI(t)
	out, _ := runCLI(t, work, c, argv, "ANTHROPIC_API_KEY=dummy-not-a-key", "ANTHROPIC_BASE_URL="+url)
	in := cliInitEvent(t, out)
	if !slices.Contains(in.Agents, "planted-agent") {
		t.Errorf("control: planted agent not listed: %v", in.Agents)
	}
	if !slices.Contains(in.Skills, "planted-skill") {
		t.Errorf("control: planted skill not listed: %v", in.Skills)
	}
	if in.OutputStyle != "planted-style" {
		t.Errorf("control: output_style = %q", in.OutputStyle)
	}
	m := markerNames(t, markers)
	for _, want := range []string{"hook-SessionStart", "mcp"} {
		if !slices.Contains(m, want) {
			t.Errorf("control: marker %s missing: %v", want, m)
		}
	}
	bs := bodies()
	for _, want := range []string{"PLANTED_CLAUDE_MD", "PLANTED_RULES", "PLANTED_PARENT", "gitStatus"} {
		if !anyContains(bs, want) {
			t.Errorf("control: %s not in any request body", want)
		}
	}

	awork := plantAgentsRepo(t)
	url, bodies = fakeAPI(t)
	runCLI(t, awork, c, argv, "ANTHROPIC_API_KEY=dummy-not-a-key", "ANTHROPIC_BASE_URL="+url)
	if !anyContains(bodies(), "PLANTED_AGENTS_MD") {
		t.Error("control: PLANTED_AGENTS_MD not in any request body")
	}
}
