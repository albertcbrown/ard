package claude

// Fixtures in testdata/.
//
// real_*.jsonl (and their .meta.json) are scrubbed captures from the pinned
// CLI (TestedVersion), made with the capture harness (capture_test.go, build
// tags integration and capture) and scrubbed by scrub_test.go; testdata/CAPTURE.md
// has the commands. TestParseReal checks them and TestRealFixturesScrubbed
// re-runs the scrubber's checks over them. Until a human commits them, the
// synthetic_* files stand in; the success, max-turns, budget and auth ones are
// deleted in the commit that adds their real counterparts (the tests that read
// them skip a file that is gone).
//
// synthetic_* files are hand-written from the documented stream-json formats
// (headless docs: system/api_retry; Agent SDK: rate_limit_event and the
// assistant message error; the errors page: "You've hit your ... limit"). They
// cost nothing and cover shapes a paid run cannot be asked for on demand:
// every limit case, the injection guard and token usage. synthetic_truncated
// stays on purpose: a killed run has no real transcript worth keeping.
//
// Observed for free on 2.1.328 (TestCLIRateLimitStream, a loopback fake that
// answers 429): the run retries (api_retry, error rate_limit), emits a
// rate_limit_event with status "rejected" and no resetsAt, and exits 1 after
// about a second with is_error, api_error_status 429 and "(not your usage
// limit)" in the text. synthetic_throttle_not_usage_limit is modelled on it.
//
// An expired OAuth token cannot be produced on demand, so real_auth_bad_token
// (a made-up token, rejected with 401) stands in for it.
//
// Usage-limit behaviour of a real "claude -p" run is NOT yet observed. A real
// capture (real_usage_limit_<scope>.jsonl, taken while already limited, see
// CAPTURE.md) answers: the exit code, the message text, whether the run waited
// or exited, and whether rate_limit_event carries resetsAt. Until then
// synthetic_usage_limit stands in, and every limit case without a reset time
// pauses with a backoff (internal/limits).

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

// fixtureOpt is fixture for a synthetic file that its real counterpart
// replaces: ok is false once the file is gone.
func fixtureOpt(name string) (string, bool) {
	b, err := os.ReadFile("testdata/" + name)
	return string(b), err == nil
}

func TestParse(t *testing.T) {
	garbage := "not json\n\n{\"type\":\"assistant\"}\nnull\n"
	big := strings.Repeat("x", 5<<20) + "\n"
	init := `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.328"}` + "\n"
	res := func(extra string) string {
		return `{"type":"result","subtype":"success","is_error":false,"num_turns":2,"total_cost_usd":0.1` + extra + "}\n"
	}
	optional := map[string]string{} // case name -> in, for files a real capture replaces
	for name, file := range map[string]string{"success": "synthetic_success.jsonl", "auth failure": "synthetic_auth_failure.jsonl",
		"max turns": "synthetic_error_max_turns.jsonl", "budget": "synthetic_budget_exceeded.jsonl"} {
		if in, ok := fixtureOpt(file); ok {
			optional[name] = in
		}
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
		{name: "success", in: optional["success"], ok: true, subtype: "success", turns: ip(7), cost: fp(0.42), reason: "completed", session: "sess-1b"},
		{name: "auth failure", in: optional["auth failure"], exit: 1, subtype: "success", isErr: true, turns: ip(1), cost: fp(0), reason: "api_error", session: "s"},
		{name: "max turns", in: optional["max turns"], exit: 1, subtype: "error_max_turns", isErr: true, turns: ip(5), cost: fp(1.5), reason: "max_turns", session: "s"},
		{name: "budget", in: optional["budget"], exit: 1, subtype: "error_max_budget_usd", isErr: true, turns: ip(3), cost: fp(2.01), session: "s"},
		{name: "truncated", in: fixture(t, "synthetic_truncated.jsonl"), exit: 137, session: "s", skipped: 1, wantErr: agent.ErrNoResult},
		{name: "empty", in: "", wantErr: agent.ErrNoResult},
		{name: "garbage interleaved", in: garbage + init + garbage + res(""), ok: true, subtype: "success", turns: ip(2), cost: fp(0.1), session: "s", skipped: 4},
		{name: "overlong line before result", in: init + big + res(""), ok: true, subtype: "success", turns: ip(2), cost: fp(0.1), session: "s", skipped: 1},
		{name: "last result wins", in: init + `{"type":"result","subtype":"error_max_turns","is_error":true}` + "\n" + res(""), ok: true, subtype: "success", turns: ip(2), cost: fp(0.1), session: "s"},
		{name: "429 is transient", in: init + `{"type":"result","subtype":"success","is_error":true,"api_error_status":429}` + "\n", exit: 1, subtype: "success", isErr: true, session: "s", transient: true},
		{name: "400 is not transient", in: init + `{"type":"result","subtype":"success","is_error":true,"api_error_status":400}` + "\n", exit: 1, subtype: "success", isErr: true, session: "s"},
		{name: "exit 0 with is_error", in: init + `{"type":"result","subtype":"success","is_error":true}` + "\n", subtype: "success", isErr: true, session: "s"},
		{name: "exit nonzero with success result", in: init + res(""), exit: 2, subtype: "success", turns: ip(2), cost: fp(0.1), session: "s"},
		{name: "malformed final result", in: init + res("") + `{"type":"result",` + "\n", exit: 0, session: "s", skipped: 1, wantErr: agent.ErrNoResult},
		{name: "no trailing newline", in: init + strings.TrimSuffix(res(""), "\n"), ok: true, subtype: "success", turns: ip(2), cost: fp(0.1), session: "s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, wasOptional := map[string]bool{"success": true, "auth failure": true, "max turns": true, "budget": true}[tc.name]; wasOptional && tc.in == "" {
				t.Skip("synthetic fixture replaced by a real capture")
			}
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
	in, ok := fixtureOpt("synthetic_success.jsonl")
	if !ok {
		t.Skip("synthetic fixture replaced by a real capture")
	}
	r, err := New(Options{}).Parse(strings.NewReader(in), 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.CLIVersion != "2.1.328" || r.Model != "claude-test" || r.PermissionDenials != 1 || r.DurationMS != 4200 || r.Summary != "done" {
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
