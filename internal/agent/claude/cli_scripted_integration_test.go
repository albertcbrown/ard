//go:build integration

// These tests run the real CLI against a scripted loopback fake of the
// Messages API. The fake streams tool_use blocks, so the CLI really runs
// Bash, Read, Grep, Edit and Write. Credentials are dummies and no paid call
// is made (#125). They pin the shipped behaviour: the adapter leaves
// CLAUDE_CODE_SUBPROCESS_ENV_SCRUB unset (see docs/proposal.md), so the CLI
// runs in dontAsk mode and Bash inherits the API key (not the OAuth token). Re-run
// them on every TestedVersion bump.
package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
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

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

// toolStep is one scripted tool call.
type toolStep struct {
	Name  string
	Input map[string]any
}

// scriptedAPI answers each request that carries tools with the next step as a
// streamed tool_use block, chosen by counting the tool_result blocks already
// in the request. Past the last step it ends the turn with text. Any other
// request gets a 400. Usage is tiny so --max-budget-usd never trips.
func scriptedAPI(t *testing.T, steps []toolStep) (url string, headers func() []http.Header) {
	t.Helper()
	var mu sync.Mutex
	var hdrs []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Tools    []json.RawMessage `json:"tools"`
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if !strings.HasPrefix(r.URL.Path, "/v1/messages") || json.Unmarshal(b, &req) != nil || len(req.Tools) == 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"fake"}}`))
			return
		}
		mu.Lock()
		hdrs = append(hdrs, r.Header.Clone())
		mu.Unlock()
		done := 0
		for _, m := range req.Messages {
			done += bytes.Count(m.Content, []byte(`"tool_result"`))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		ev := func(name string, v any) {
			j, _ := json.Marshal(v)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, j)
		}
		ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": "msg_fake", "type": "message", "role": "assistant", "model": "claude-fake", "content": []any{},
			"stop_reason": nil, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}}})
		stop := "end_turn"
		if done < len(steps) {
			s := steps[done]
			in, _ := json.Marshal(s.Input)
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{
				"type": "tool_use", "id": fmt.Sprintf("toolu_%02d", done), "name": s.Name, "input": map[string]any{}}})
			ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{
				"type": "input_json_delta", "partial_json": string(in)}})
			stop = "tool_use"
		} else {
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
			ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "done"}})
		}
		ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		ev("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
			"usage": map[string]int{"output_tokens": 1}})
		ev("message_stop", map[string]any{"type": "message_stop"})
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []http.Header {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(hdrs)
	}
}

type toolResult struct {
	ID      string
	IsError bool
	Text    string
}

// toolResults returns the tool_result blocks of the stream-json user events,
// in order.
func toolResults(t *testing.T, out []byte) []toolResult {
	t.Helper()
	var rs []toolResult
	for _, l := range bytes.Split(out, []byte("\n")) {
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(l, &ev) != nil || ev.Type != "user" {
			continue
		}
		var blocks []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			IsError   bool            `json:"is_error"`
			Content   json.RawMessage `json:"content"`
		}
		if json.Unmarshal(ev.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_result" {
				rs = append(rs, toolResult{b.ToolUseID, b.IsError, string(b.Content)})
			}
		}
	}
	return rs
}

type scriptResult struct {
	Type              string            `json:"type"`
	PermissionDenials []json.RawMessage `json:"permission_denials"`
}

func finalResult(t *testing.T, out []byte) scriptResult {
	t.Helper()
	for _, l := range bytes.Split(out, []byte("\n")) {
		var r scriptResult
		if json.Unmarshal(l, &r) == nil && r.Type == "result" {
			return r
		}
	}
	t.Fatalf("no result event (a hang or crash) in %s", out)
	return scriptResult{}
}

// scriptedCommand is workerArgvAuth with an optional explicit tool list.
func scriptedCommand(t *testing.T, auth agent.Auth, tools []string) agent.Command {
	t.Helper()
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not on PATH")
	}
	prompt := filepath.Join(t.TempDir(), "prompt.txt")
	writeFile(t, prompt, "test")
	c, err := New(Options{Binary: bin}).Command(agent.Spec{Stage: agent.StageWorker, Role: "default", Issue: 1,
		PromptFile: prompt, ConfigDir: t.TempDir(), MaxBudgetUSD: 0.01, Auth: auth, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// scriptRepo is a git worktree with a.txt and package.json, with a separate
// markers directory outside it.
func scriptRepo(t *testing.T) (work, markers string) {
	t.Helper()
	root := t.TempDir()
	work, markers = filepath.Join(root, "work"), filepath.Join(root, "markers")
	writeFile(t, filepath.Join(work, "a.txt"), "alpha\n")
	writeFile(t, filepath.Join(work, "package.json"), "{}\n")
	writeFile(t, filepath.Join(markers, ".keep"), "")
	gittest.Run(t, work, "init", "--initial-branch=main")
	gittest.Run(t, work, "add", "-A")
	gittest.Run(t, work, "commit", "-m", "seed")
	return work, markers
}

// With the scrub unset, the CLI authenticates with the job's credential. The
// API key reaches Bash children; the OAuth token is removed from them by the
// CLI but stays in the parent claude process's env. This pins the accepted
// residual risk (the container is the boundary) for the tested release.
func TestCLIBashInheritsCredential(t *testing.T) {
	const key, tok = "dummy-not-a-key-0000", "ard-dummy-oauth-not-a-token-0000"
	for _, tc := range []struct {
		name      string
		auth      agent.Auth
		env       string
		value     string
		inherited bool
	}{
		{"api key", agent.AuthAPIKey, "ANTHROPIC_API_KEY", key, true},
		{"oauth token", agent.AuthOAuthToken, "CLAUDE_CODE_OAUTH_TOKEN", tok, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work, markers := scriptRepo(t)
			envFile := filepath.Join(markers, "env.txt")
			url, headers := scriptedAPI(t, []toolStep{{"Bash", map[string]any{"command": "env > " + envFile}}})
			c := scriptedCommand(t, tc.auth, nil)
			out, _ := runCLI(t, work, c, c.Argv, tc.env+"="+tc.value, "ANTHROPIC_BASE_URL="+url)
			if in := cliInitEvent(t, out); in.PermissionMode != "dontAsk" {
				t.Errorf("permissionMode = %q, want dontAsk", in.PermissionMode)
			}
			finalResult(t, out)
			b, err := os.ReadFile(envFile)
			if err != nil {
				t.Fatalf("Bash did not run: %v\n%s", err, out)
			}
			for _, s := range []string{tc.env, tc.value} {
				if got := bytes.Contains(b, []byte(s)); got != tc.inherited {
					t.Errorf("Bash env contains %q = %v, want %v", s, got, tc.inherited)
				}
			}
			hs := headers()
			if len(hs) == 0 {
				t.Fatal("no request reached the fake API")
			}
			for _, h := range hs {
				if tc.auth == agent.AuthAPIKey && h.Get("X-Api-Key") != key || tc.auth == agent.AuthOAuthToken && h.Get("Authorization") != "Bearer "+tok {
					t.Errorf("credential not sent: %v", h)
				}
			}
		})
	}
}

// Under dontAsk every allow and deny outcome follows the adapter's explicit
// --allowed-tools and --disallowed-tools, and nothing may wait for a prompt.
func TestCLIDontAskOutcomes(t *testing.T) {
	type step struct {
		toolStep
		denied bool
	}
	for _, tc := range []struct {
		name  string
		tools []string
		steps func(work, markers string) []step
	}{
		{"default tools", nil, func(work, markers string) []step {
			return []step{
				{toolStep{"Bash", map[string]any{"command": "echo ran"}}, false},
				{toolStep{"Write", map[string]any{"file_path": filepath.Join(work, "new.txt"), "content": "x"}}, false},
				{toolStep{"Write", map[string]any{"file_path": filepath.Join(work, "package.json"), "content": "{\"a\":1}"}}, false},
				{toolStep{"Write", map[string]any{"file_path": filepath.Join(markers, "outside.txt"), "content": "x"}}, true},
				{toolStep{"Bash", map[string]any{"command": "git push origin main"}}, true},
				{toolStep{"Edit", map[string]any{"file_path": filepath.Join(work, "a.txt"), "old_string": "alpha", "new_string": "beta"}}, false},
			}
		}},
		{"narrow tools", []string{"Read", "Grep", "Bash(echo *)"}, func(work, markers string) []step {
			return []step{
				{toolStep{"Read", map[string]any{"file_path": filepath.Join(work, "a.txt")}}, false},
				{toolStep{"Grep", map[string]any{"pattern": "alpha", "path": work}}, false},
				{toolStep{"Bash", map[string]any{"command": "ls"}}, false},
				{toolStep{"Bash", map[string]any{"command": "env"}}, true},
				{toolStep{"Bash", map[string]any{"command": "echo x > package.json"}}, true},
				{toolStep{"Bash", map[string]any{"command": "echo hi"}}, false},
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work, markers := scriptRepo(t)
			ss := tc.steps(work, markers)
			var script []toolStep
			var want []bool
			for _, s := range ss {
				script = append(script, s.toolStep)
				want = append(want, s.denied)
			}
			url, _ := scriptedAPI(t, script)
			c := scriptedCommand(t, agent.AuthAPIKey, tc.tools)
			out, _ := runCLI(t, work, c, c.Argv, "ANTHROPIC_API_KEY=dummy-not-a-key", "ANTHROPIC_BASE_URL="+url)
			if in := cliInitEvent(t, out); in.PermissionMode != "dontAsk" {
				t.Errorf("permissionMode = %q, want dontAsk", in.PermissionMode)
			}
			res := finalResult(t, out) // a result event proves nothing hung
			nd := 0
			for _, d := range want {
				if d {
					nd++
				}
			}
			if len(res.PermissionDenials) != nd {
				t.Errorf("permission_denials = %d, want %d", len(res.PermissionDenials), nd)
			}
			var got []bool
			for _, r := range toolResults(t, out) {
				got = append(got, r.IsError)
			}
			if !slices.Equal(got, want) {
				t.Errorf("tool errors = %v, want %v\n%s", got, want, out)
			}
		})
	}
}
