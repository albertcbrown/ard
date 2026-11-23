package claude

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
)

func parseStr(t *testing.T, in string) (agent.Result, error) {
	t.Helper()
	return (&Adapter{}).Parse(strings.NewReader(in), 0)
}

const okResult = `{"type":"result","subtype":"success","is_error":false,"num_turns":1,"total_cost_usd":0.01}` + "\n"

func TestParseWindows(t *testing.T) {
	res, err := parseStr(t, fixture(t, "synthetic_windows.jsonl"))
	if err != nil || !res.OK {
		t.Fatalf("err=%v ok=%v", err, res.OK)
	}
	if len(res.Windows) != 2 || res.Windows[0].Type != "five_hour" || res.Windows[1].Type != "seven_day" {
		t.Fatalf("windows = %+v", res.Windows)
	}
	w := res.Windows[0]
	if w.Utilization != 0.1 || w.Status != "allowed" || !w.ResetsAt.Equal(time.Unix(1789990000, 0)) || w.ResetsAt.Location() != time.UTC {
		t.Errorf("five_hour = %+v", w)
	}
}

func TestParseWindowsLastWins(t *testing.T) {
	in := `{"type":"rate_limit_event","rate_limit_info":{"unifiedWindows":{"five_hour":{"utilization":0.1}}}}` + "\n" +
		`{"type":"rate_limit_event","rate_limit_info":{"unifiedWindows":[{"type":"five_hour","utilization":0.5,"status":"allowed_warning"}]}}` + "\n" + okResult
	res, _ := parseStr(t, in)
	if len(res.Windows) != 1 || res.Windows[0].Utilization != 0.5 || res.Windows[0].Status != "allowed_warning" {
		t.Fatalf("windows = %+v", res.Windows)
	}
}

func TestParseWindowsMalformed(t *testing.T) {
	for _, raw := range []string{`"x"`, `7`, `{"five_hour":"bad"}`, `{"five_hour":{"utilization":-1}}`, `{"five_hour":{}}`} {
		in := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":` + raw + `}}` + "\n" + okResult
		res, err := parseStr(t, in)
		if err != nil || !res.OK || res.SkippedLines != 0 || len(res.Windows) != 0 {
			t.Errorf("%s: err=%v ok=%v skipped=%d windows=%+v", raw, err, res.OK, res.SkippedLines, res.Windows)
		}
	}
}

func TestParseWindowsNoResult(t *testing.T) {
	in := `{"type":"rate_limit_event","rate_limit_info":{"unifiedWindows":{"seven_day":{"utilization":0.9}}}}` + "\n"
	res, err := parseStr(t, in)
	if !errors.Is(err, agent.ErrNoResult) || len(res.Windows) != 1 {
		t.Fatalf("err=%v windows=%+v", err, res.Windows)
	}
}

func TestParseWindowsMillis(t *testing.T) {
	in := `{"type":"rate_limit_event","rate_limit_info":{"unifiedWindows":{"five_hour":{"utilization":0.2,"resetsAt":1789990000000}}}}` + "\n" + okResult
	res, _ := parseStr(t, in)
	if len(res.Windows) != 1 || !res.Windows[0].ResetsAt.Equal(time.Unix(1789990000, 0)) {
		t.Fatalf("windows = %+v", res.Windows)
	}
}
