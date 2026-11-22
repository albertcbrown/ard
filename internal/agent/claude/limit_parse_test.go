package claude

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
)

func TestParseLimits(t *testing.T) {
	reset := time.Unix(1795000000, 0).UTC()
	hit := func(scope agent.LimitScope, family string, at time.Time, sig agent.LimitSignal) *agent.LimitHit {
		return &agent.LimitHit{Scope: scope, Family: family, ResetsAt: at, Signal: sig}
	}
	tests := []struct {
		name    string
		in      string
		exit    int
		ok      bool
		wantErr error
		limit   *agent.LimitHit
	}{
		{name: "rejected event", in: fixture(t, "synthetic_limit_rejected_event.jsonl"), exit: 1,
			limit: hit(agent.ScopeSession, "", reset, agent.SignalRateLimitEvent)},
		{name: "opus text, no event, curly apostrophe", in: fixture(t, "synthetic_limit_opus_text.jsonl"), exit: 1,
			limit: hit(agent.ScopeModel, "opus", time.Time{}, agent.SignalLimitMessage)},
		{name: "resets in milliseconds", in: fixture(t, "synthetic_limit_resets_ms.jsonl"), exit: 1,
			limit: hit(agent.ScopeWeekly, "", reset, agent.SignalRateLimitEvent)},
		{name: "killed while retrying: no result", in: fixture(t, "synthetic_api_retry_killed.jsonl"), exit: 137, wantErr: agent.ErrNoResult,
			limit: hit(agent.ScopeUnknown, "", time.Time{}, agent.SignalRateLimitError)},
		{name: "recovered keeps the hit and is OK", in: fixture(t, "synthetic_api_retry_recovered.jsonl"), ok: true,
			limit: hit(agent.ScopeUnknown, "", time.Time{}, agent.SignalRateLimitError)},
		{name: "warning only", in: fixture(t, "synthetic_limit_warning_only.jsonl"), ok: true},
		{name: "credits required", in: fixture(t, "synthetic_credits_required.jsonl"), exit: 1,
			limit: hit(agent.ScopeUnknown, "", time.Time{}, agent.SignalRateLimitEvent)},
		{name: "agent quotes the limit text: injection guard", in: fixture(t, "synthetic_agent_quotes_limit.jsonl"), ok: true},
		// Modelled on a real 2.1.328 run against a 429 loopback fake: the CLI
		// emits a rejected rate_limit_event and api_retry rate_limit for a plain
		// throttle too, but says "not your usage limit".
		{name: "throttle is not a usage limit", in: fixture(t, "synthetic_throttle_not_usage_limit.jsonl"), exit: 1},
		{name: "usage limit stand-in", in: fixture(t, "synthetic_usage_limit.jsonl"), exit: 1,
			limit: hit(agent.ScopeWeekly, "", reset, agent.SignalRateLimitEvent)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := New(Options{}).Parse(strings.NewReader(tc.in), tc.exit)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if r.OK != tc.ok {
				t.Errorf("OK = %v, want %v", r.OK, tc.ok)
			}
			switch {
			case tc.limit == nil && r.LimitHit != nil:
				t.Errorf("LimitHit = %+v, want nil", *r.LimitHit)
			case tc.limit != nil && r.LimitHit == nil:
				t.Errorf("LimitHit = nil, want %+v", *tc.limit)
			case tc.limit != nil && *r.LimitHit != *tc.limit:
				t.Errorf("LimitHit = %+v, want %+v", *r.LimitHit, *tc.limit)
			}
		})
	}
}

// Text is matched only on CLI-synthesised error paths (invariant 6).
func TestParseLimitTextOnlyOnErrorPaths(t *testing.T) {
	init := `{"type":"system","subtype":"init","session_id":"s"}` + "\n"
	phrase := `You've hit your session limit · resets 3:45pm`
	assistant := func(extra string) string {
		return `{"type":"assistant"` + extra + `,"message":{"content":[{"type":"text","text":"` + phrase + `"}]}}` + "\n"
	}
	result := func(fields string) string {
		return `{"type":"result","subtype":"success",` + fields + `,"result":"` + phrase + `"}` + "\n"
	}
	tests := []struct {
		name string
		in   string
		want agent.LimitSignal // "" means no LimitHit
	}{
		{"assistant without error", init + assistant("") + result(`"is_error":false`), ""},
		{"assistant with an error that is not a limit still matches text", init + assistant(`,"error":"server_error"`) + result(`"is_error":false`), agent.SignalLimitMessage},
		{"result not is_error", init + result(`"is_error":false,"api_error_status":429,"terminal_reason":"api_error"`), ""},
		{"is_error result with 400", init + result(`"is_error":true,"api_error_status":400`), ""},
		{"is_error result with terminal_reason api_error", init + result(`"is_error":true,"terminal_reason":"api_error"`), agent.SignalLimitMessage},
		{"is_error result with 429", init + result(`"is_error":true,"api_error_status":429`), agent.SignalLimitMessage},
		{"throttle text with a reset time is still a limit", init + `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1795000000}}` + "\n" + `{"type":"assistant","error":"rate_limit","message":{"content":[{"type":"text","text":"not your usage limit"}]}}` + "\n", agent.SignalRateLimitEvent},
		{"throttle text alone is not", init + `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected"}}` + "\n" + `{"type":"assistant","error":"rate_limit","message":{"content":[{"type":"text","text":"Server is temporarily limiting requests (not your usage limit)"}]}}` + "\n", ""},
		{"unknown rateLimitType keeps text scope", init + `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","rateLimitType":"overage"}}` + "\n" + assistant(`,"error":"rate_limit"`), agent.SignalRateLimitEvent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := New(Options{}).Parse(strings.NewReader(tc.in), 1)
			switch {
			case tc.want == "" && r.LimitHit != nil:
				t.Errorf("LimitHit = %+v, want nil", *r.LimitHit)
			case tc.want != "" && (r.LimitHit == nil || r.LimitHit.Signal != tc.want):
				t.Errorf("LimitHit = %+v, want signal %s", r.LimitHit, tc.want)
			}
		})
	}
}

func TestParseRateLimitEventDetails(t *testing.T) {
	init := `{"type":"system","subtype":"init","session_id":"s"}` + "\n"
	ev := func(info string) string { return `{"type":"rate_limit_event","rate_limit_info":` + info + `}` + "\n" }
	tests := []struct {
		name   string
		in     string
		scope  agent.LimitScope
		family string
		reset  time.Time
	}{
		{"five_hour", ev(`{"status":"rejected","resetsAt":1795000000,"rateLimitType":"five_hour"}`), agent.ScopeSession, "", time.Unix(1795000000, 0).UTC()},
		{"seven_day_opus", ev(`{"status":"rejected","rateLimitType":"seven_day_opus"}`), agent.ScopeModel, "opus", time.Time{}},
		{"seven_day_sonnet", ev(`{"status":"rejected","rateLimitType":"seven_day_sonnet"}`), agent.ScopeModel, "sonnet", time.Time{}},
		{"zero reset is none", ev(`{"status":"rejected","resetsAt":0}`), "", "", time.Time{}},
		{"negative reset is none", ev(`{"status":"rejected","resetsAt":-5}`), "", "", time.Time{}},
		{"latest non-zero reset wins", ev(`{"status":"rejected","resetsAt":1795000000}`) + ev(`{"status":"rejected","resetsAt":1795000600}`) + ev(`{"status":"rejected"}`), "", "", time.Unix(1795000600, 0).UTC()},
		{"unknown type leaves scope unset", ev(`{"status":"rejected","rateLimitType":"overage"}`), "", "", time.Time{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := New(Options{}).Parse(strings.NewReader(init+tc.in), 1)
			if r.LimitHit == nil {
				t.Fatal("no LimitHit")
			}
			if r.LimitHit.Scope != tc.scope || r.LimitHit.Family != tc.family || !r.LimitHit.ResetsAt.Equal(tc.reset) {
				t.Errorf("got %+v", *r.LimitHit)
			}
		})
	}
	t.Run("malformed new event skips only that line", func(t *testing.T) {
		in := init + `{"type":"rate_limit_event","rate_limit_info":"nope"}` + "\n" + `{"type":"result","subtype":"success"}` + "\n"
		r, err := New(Options{}).Parse(strings.NewReader(in), 0)
		if err != nil || !r.OK || r.SkippedLines != 1 || r.LimitHit != nil {
			t.Errorf("got %+v, %v", r, err)
		}
	})
}

func TestParseUsage(t *testing.T) {
	r, err := New(Options{}).Parse(strings.NewReader(fixture(t, "synthetic_usage.jsonl")), 0)
	if err != nil {
		t.Fatal(err)
	}
	// Usage is the sum of modelUsage (null counts as 0), not result.usage.
	want := agent.Usage{Input: 110, Output: 55, CacheWrite: 3, CacheRead: 22}
	if r.Usage == nil || *r.Usage != want {
		t.Errorf("Usage = %+v, want %+v", r.Usage, want)
	}
	if len(r.ModelUsage) != 2 || r.ModelUsage["claude-opus-test"] != (agent.Usage{Input: 100, Output: 50, CacheRead: 20}) {
		t.Errorf("ModelUsage = %+v", r.ModelUsage)
	}
	t.Run("falls back to result.usage", func(t *testing.T) {
		in := `{"type":"result","subtype":"success","usage":{"input_tokens":7,"output_tokens":3,"cache_creation_input_tokens":null,"cache_read_input_tokens":1}}` + "\n"
		r, _ := New(Options{}).Parse(strings.NewReader(in), 0)
		if r.Usage == nil || *r.Usage != (agent.Usage{Input: 7, Output: 3, CacheRead: 1}) || r.ModelUsage != nil {
			t.Errorf("got %+v / %+v", r.Usage, r.ModelUsage)
		}
	})
	t.Run("absent and malformed stay nil without dropping the result", func(t *testing.T) {
		for _, extra := range []string{``, `,"usage":"x","modelUsage":[1]`, `,"usage":null,"modelUsage":null`} {
			in := `{"type":"result","subtype":"success"` + extra + "}\n"
			r, err := New(Options{}).Parse(strings.NewReader(in), 0)
			if err != nil || !r.OK || r.Usage != nil || r.ModelUsage != nil {
				t.Errorf("%q: got %+v, %v", extra, r, err)
			}
		}
	})
}
