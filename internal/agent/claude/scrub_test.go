package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The scrubber turns a raw capture (see capture_test.go) into a committable
// fixture. It is an allowlist of replacements followed by a denylist check:
// nothing passes silently, and a line the checks still flag fails the scrub.
// It has no build tag so it is unit-tested with `make test`.

// bogusToken is the made-up credential the bad-token capture sends.
const bogusToken = "ard-invalid-oauth-token-00000000000000"

// scrubEnv holds what a capture run knew about its own machine.
type scrubEnv struct {
	Work, Home, ConfigDir, TmpDir, PromptPath string
	UserHome, User                            string   // os.UserHomeDir() and the user name
	Secrets                                   []string // credential values that must not appear
}

var (
	// idKeys are the keys whose string values are replaced, by namespace.
	idKeyRE = regexp.MustCompile(`"(session_id|uuid|parent_uuid|request_id|id|tool_use_id|message_id|timestamp|cwd)":"((?:[^"\\]|\\.)*)"`)

	// sigRE matches a thinking block's signature: an opaque, account-bound
	// blob the parser never reads, replaced with a fixed placeholder.
	sigRE = regexp.MustCompile(`"signature":"(?:[^"\\]|\\.)*"`)

	uuidRE  = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	emailRE = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)
	homeRE  = regexp.MustCompile(`/Users/|/home/[A-Za-z0-9_][A-Za-z0-9._-]*`)
)

// scrubTranscript rewrites a raw stream-json capture. Every line must be valid
// JSON. The rewrite works on the raw line, key by key, so key order is kept
// (the parser's {"type":"result" prefix check depends on it).
func scrubTranscript(raw []byte, env scrubEnv) ([]byte, error) {
	ids := map[string]map[string]string{"session": {}, "uuid": {}, "msg": {}}
	place := func(ns, orig string) string {
		m := ids[ns]
		if v, ok := m[orig]; ok {
			return v
		}
		n := len(m) + 1
		var v string
		switch ns {
		case "session":
			v = fmt.Sprintf("session-%d", n)
		case "uuid":
			v = fmt.Sprintf("uuid-%04d", n)
		default:
			v = fmt.Sprintf("msg-%04d", n)
		}
		m[orig] = v
		return v
	}
	paths := pathReplacements(env)
	var out bytes.Buffer
	for i, line := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if !json.Valid(line) {
			return nil, fmt.Errorf("scrub: line %d is not valid JSON", i+1)
		}
		line = idKeyRE.ReplaceAllFunc(line, func(m []byte) []byte {
			sub := idKeyRE.FindSubmatch(m)
			key, val := string(sub[1]), string(sub[2])
			var repl string
			switch key {
			case "session_id":
				repl = place("session", val)
			case "uuid", "parent_uuid":
				repl = place("uuid", val)
			case "timestamp":
				repl = "2026-01-01T00:00:00Z"
			case "cwd":
				repl = "/work"
			default:
				repl = place("msg", val)
			}
			return []byte(`"` + key + `":"` + repl + `"`)
		})
		line = sigRE.ReplaceAll(line, []byte(`"signature":"sig-redacted"`))
		for _, p := range paths {
			line = bytes.ReplaceAll(line, []byte(p.from), []byte(p.to))
		}
		if err := postCheck(line, env); err != nil {
			return nil, fmt.Errorf("scrub: line %d: %w", i+1, err)
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}

type pathRepl struct{ from, to string }

// pathReplacements lists each known path, and its symlink-resolved form, with
// its placeholder. Longer paths go first so a path under HOME is not cut short.
func pathReplacements(env scrubEnv) []pathRepl {
	var rs []pathRepl
	add := func(p, to string) {
		p = strings.TrimRight(p, "/")
		if p == "" || p == "." {
			return
		}
		rs = append(rs, pathRepl{p, to})
		if real, err := filepath.EvalSymlinks(p); err == nil && real != p {
			rs = append(rs, pathRepl{real, to})
		}
	}
	add(env.PromptPath, "/prompt.txt")
	add(env.Work, "/work")
	add(env.ConfigDir, "/config")
	add(env.Home, "/home")
	add(env.TmpDir, "/tmp")
	add(env.UserHome, "/home")
	sort.SliceStable(rs, func(a, b int) bool { return len(rs[a].from) > len(rs[b].from) })
	return rs
}

// postCheck is the denylist: what must not survive in a committed fixture.
func postCheck(line []byte, env scrubEnv) error {
	if m := uuidRE.Find(line); m != nil {
		return fmt.Errorf("a UUID survived (%s)", m)
	}
	if homeRE.Match(line) {
		return fmt.Errorf("a home path survived")
	}
	if emailRE.Match(line) {
		return fmt.Errorf("an email address survived")
	}
	if bytes.Contains(line, []byte("sk-ant-")) {
		return fmt.Errorf("an sk-ant- key survived")
	}
	secrets := append([]string{bogusToken}, env.Secrets...)
	for _, s := range secrets {
		if s != "" && bytes.Contains(line, []byte(s)) {
			return fmt.Errorf("a credential value survived")
		}
	}
	// A short user name matches ordinary words; the home-path check covers it.
	if n := env.User; len(n) >= 4 && regexp.MustCompile(`\b`+regexp.QuoteMeta(n)+`\b`).Match(line) {
		return fmt.Errorf("the user name survived")
	}
	return nil
}

// currentScrubEnv fills the machine-specific parts for a capture run.
func currentScrubEnv(work, home, configDir, prompt string, secrets ...string) scrubEnv {
	env := scrubEnv{Work: work, Home: home, ConfigDir: configDir, TmpDir: os.TempDir(), PromptPath: prompt, Secrets: secrets}
	env.UserHome, _ = os.UserHomeDir()
	if u, err := user.Current(); err == nil {
		env.User = u.Username
	}
	return env
}

func TestScrubTranscript(t *testing.T) {
	env := scrubEnv{Work: "/var/work/job1", Home: "/var/home/job1", ConfigDir: "/var/cfg/job1", TmpDir: "/var/tmp/x",
		PromptPath: "/var/prompt/p.txt", UserHome: "/Users/jane", User: "janedoe", Secrets: []string{"tok-live-123"}}
	raw := `{"type":"system","subtype":"init","cwd":"/var/work/job1","session_id":"a1b2c3d4-0000-4000-8000-000000000001","uuid":"11111111-2222-4333-8444-555555555555","tools":["Read"]}
{"type":"assistant","uuid":"66666666-7777-4888-8999-000000000000","session_id":"a1b2c3d4-0000-4000-8000-000000000001","timestamp":"2026-11-22T10:11:12.345Z","message":{"id":"msg_01ABC","content":[{"type":"tool_use","id":"toolu_01XYZ","name":"Read","input":{"file_path":"/var/work/job1/README.md"}}]}}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"","signature":"CAQSnh+/AbC=="}]}}
{"type":"user","parent_uuid":"11111111-2222-4333-8444-555555555555","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01XYZ","content":"capture fixture in /var/tmp/x/z and /var/cfg/job1/s"}]}}
{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1795000000},"uuid":"11111111-2222-4333-8444-555555555555","session_id":"a1b2c3d4-0000-4000-8000-000000000001"}
{"type":"result","subtype":"success","is_error":true,"result":"You've hit your session limit · resets 3:45pm","total_cost_usd":0.01,"duration_ms":12,"usage":{"input_tokens":5},"session_id":"a1b2c3d4-0000-4000-8000-000000000001"}
`
	got, err := scrubTranscript([]byte(raw), env)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 6 {
		t.Fatalf("lines = %d", len(lines))
	}
	want := []string{
		`{"type":"system","subtype":"init","cwd":"/work","session_id":"session-1","uuid":"uuid-0001","tools":["Read"]}`,
		`{"type":"assistant","uuid":"uuid-0002","session_id":"session-1","timestamp":"2026-01-01T00:00:00Z","message":{"id":"msg-0001","content":[{"type":"tool_use","id":"msg-0002","name":"Read","input":{"file_path":"/work/README.md"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"","signature":"sig-redacted"}]}}`,
		`{"type":"user","parent_uuid":"uuid-0001","message":{"content":[{"type":"tool_result","tool_use_id":"msg-0002","content":"capture fixture in /tmp/z and /config/s"}]}}`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1795000000},"uuid":"uuid-0001","session_id":"session-1"}`,
		`{"type":"result","subtype":"success","is_error":true,"result":"You've hit your session limit · resets 3:45pm","total_cost_usd":0.01,"duration_ms":12,"usage":{"input_tokens":5},"session_id":"session-1"}`,
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d:\n got %s\nwant %s", i+1, lines[i], want[i])
		}
	}
	// What the tests assert on survives.
	for _, keep := range []string{`"resetsAt":1795000000`, `resets 3:45pm`, `"total_cost_usd":0.01`, `"duration_ms":12`, `"usage":{"input_tokens":5}`} {
		if !strings.Contains(string(got), keep) {
			t.Errorf("lost %s", keep)
		}
	}
	// Scrubbing the scrubbed output is a no-op.
	again, err := scrubTranscript(got, env)
	if err != nil || !bytes.Equal(again, got) {
		t.Errorf("not idempotent: %v", err)
	}
}

func TestScrubTranscriptFailsClosed(t *testing.T) {
	env := scrubEnv{Work: "/var/work/job1", UserHome: "/Users/jane", User: "janedoe", Secrets: []string{"tok-live-123"}}
	tests := []struct {
		name string
		line string
	}{
		{"a UUID in an unlisted key", `{"type":"x","other":"11111111-2222-4333-8444-555555555555"}`},
		{"a UUID in text", `{"type":"assistant","text":"id 11111111-2222-4333-8444-555555555555"}`},
		{"a home path of another user", `{"type":"x","path":"/Users/bob/file"}`},
		{"a linux home path", `{"type":"x","path":"/home/bob/file"}`},
		{"an email", `{"type":"x","text":"mail bob@example.com"}`},
		{"an api key prefix", `{"type":"x","text":"sk-ant-api03-abc"}`},
		{"the bogus token", `{"type":"x","text":"` + bogusToken + `"}`},
		{"a secret value", `{"type":"x","text":"tok-live-123"}`},
		{"the user name", `{"type":"x","text":"hello janedoe"}`},
		{"a line that is not JSON", `not json`},
		{"a truncated line", `{"type":"x"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := scrubTranscript([]byte(tc.line+"\n"), env); err == nil {
				t.Errorf("scrub passed: %s", out)
			}
		})
	}
	if _, err := scrubTranscript([]byte(`{"type":"x","cwd":"/var/work/job1","path":"/var/work/job1/a"}`+"\n"), env); err != nil {
		t.Errorf("a clean line failed: %v", err)
	}
}
