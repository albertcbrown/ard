package claude

// The files in testdata/ are SYNTHETIC: hand-written to match the stream-json
// shape, not recorded from a real run. A human must capture real transcripts
// (success, error_max_turns, budget exceeded, auth failure) with an API key,
// scrub session_id, uuid, cwd, timestamps and file contents, and add them as
// non-synthetic samples. Capture with, for example:
//   CLAUDE_CONFIG_DIR=$(mktemp -d) ANTHROPIC_API_KEY=... claude -p --output-format=stream-json --verbose --bare \
//     --max-budget-usd=0.05 -- "say hi" > success.jsonl

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/agent"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParse(t *testing.T) {
	garbage := "not json\n\n{\"type\":\"assistant\"}\nnull\n"
	big := strings.Repeat("x", 5<<20) + "\n"
	init := `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.328"}` + "\n"
	res := func(extra string) string {
		return `{"type":"result","subtype":"success","is_error":false,"num_turns":2,"total_cost_usd":0.1` + extra + "}\n"
	}
	tests := []struct {
		name      string
		in        string
		exit      int
		ok        bool
		subtype   string
		isErr     bool
		turns     *int
		cost      *float64
		reason    string
		session   string
		skipped   int
		wantErr   error
		transient bool
	}{
		{name: "success", in: fixture(t, "synthetic_success.jsonl"), ok: true, subtype: "success", turns: ip(7), cost: fp(0.42), reason: "completed", session: "sess-1b"},
		{name: "auth failure", in: fixture(t, "synthetic_auth_failure.jsonl"), exit: 1, subtype: "success", isErr: true, turns: ip(1), cost: fp(0), reason: "api_error", session: "s"},
		{name: "max turns", in: fixture(t, "synthetic_error_max_turns.jsonl"), exit: 1, subtype: "error_max_turns", isErr: true, turns: ip(5), cost: fp(1.5), reason: "max_turns", session: "s"},
		{name: "budget", in: fixture(t, "synthetic_budget_exceeded.jsonl"), exit: 1, subtype: "error_max_budget_usd", isErr: true, turns: ip(3), cost: fp(2.01), session: "s"},
		{name: "truncated", in: fixture(t, "synthetic_truncated.jsonl"), exit: 137, session: "s", skipped: 1, wantErr: agent.ErrNoResult},
		{name: "empty", in: "", wantErr: agent.ErrNoResult},
		{name: "garbage interleaved", in: garbage + init + garbage + res(""), ok: true, subtype: "success", turns: ip(2), cost: fp(0.1), session: "s", skipped: 4},
		{name: "overlong line before result", in: init + big + res(""), ok: true, subtype: "success", turns: ip(2), cost: fp(0.1), session: "s", skipped: 1},
		{name: "last result wins", in: init + `{"type":"result","subtype":"error_max_turns","is_error":true}` + "\n" + res(""), ok: true, subtype: "success", turns: ip(2), cost: fp(0.1), session: "s"},
		{name: "429 is transient", in: init + `{"type":"result","subtype":"success","is_error":true,"api_error_status":429}` + "\n", exit: 1, subtype: "success", isErr: true, session: "s", transient: true},
		{name: "400 is not transient", in: init + `{"type":"result","subtype":"success","is_error":true,"api_error_status":400}` + "\n", exit: 1, subtype: "success", isErr: true, session: "s"},
		{name: "exit 0 with is_error", in: init + `{"type":"result","subtype":"success","is_error":true}` + "\n", subtype: "success", isErr: true, session: "s"},
		{name: "exit nonzero with success result", in: init + res(""), exit: 2, subtype: "success", turns: ip(2), cost: fp(0.1), session: "s"},
		{name: "no trailing newline", in: init + strings.TrimSuffix(res(""), "\n"), ok: true, subtype: "success", turns: ip(2), cost: fp(0.1), session: "s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := New(Options{}).Parse(strings.NewReader(tc.in), tc.exit)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if r.OK != tc.ok || r.Subtype != tc.subtype || r.IsError != tc.isErr || r.TerminalReason != tc.reason ||
				r.SessionID != tc.session || r.SkippedLines != tc.skipped || r.ExitCode != tc.exit || r.Transient() != tc.transient {
				t.Errorf("got %+v", r)
			}
			if !eq(r.Turns, tc.turns) || !eq(r.CostUSD, tc.cost) {
				t.Errorf("turns/cost = %v/%v, want %v/%v", r.Turns, r.CostUSD, tc.turns, tc.cost)
			}
		})
	}
}

func TestParseDetails(t *testing.T) {
	r, err := New(Options{}).Parse(strings.NewReader(fixture(t, "synthetic_success.jsonl")), 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.CLIVersion != TestedVersion || r.Model != "claude-test" || r.PermissionDenials != 1 || r.DurationMS != 4200 || r.Summary != "done" {
		t.Errorf("got %+v", r)
	}
	long := `{"type":"result","subtype":"success","result":"` + strings.Repeat("é", 3000) + `"}` + "\n"
	r, _ = New(Options{}).Parse(strings.NewReader(long), 0)
	if n := len([]rune(r.Summary)); n != 2000 {
		t.Errorf("summary runes = %d", n)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestParseReadError(t *testing.T) {
	if _, err := New(Options{}).Parse(errReader{}, 0); err == nil || !strings.Contains(err.Error(), "read agent stdout") {
		t.Errorf("err = %v", err)
	}
}

func ip(n int) *int         { return &n }
func fp(f float64) *float64 { return &f }
func eq[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
