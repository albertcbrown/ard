package scanner

import (
	"strings"
	"testing"
)

func TestClassOf(t *testing.T) {
	cases := []struct {
		name    string
		res     RunResult
		class   string
		failing bool
		ok      bool
	}{
		{"backpressure", RunResult{Outcome: OutcomeBackpressure}, "max_open", true, false},
		{"guard rule", RunResult{Outcome: OutcomeScanFailed, GuardRule: "aws-access-token"}, "aws-access-token", true, false},
		{"guard error", RunResult{Outcome: OutcomeScanFailed, GuardRule: "scan-error"}, "scan-error", true, false},
		{"plain scan_failed", RunResult{Outcome: OutcomeScanFailed}, "scan_failed", true, false},
		{"timeout", RunResult{Outcome: OutcomeTimeout}, "scan_failed", true, false},
		{"ok", RunResult{Outcome: OutcomeOK}, "", false, true},
		{"global cap", RunResult{Outcome: OutcomeGlobalCap}, "", false, false},
		{"error", RunResult{Outcome: OutcomeError}, "", false, false},
		{"no slot", RunResult{Outcome: OutcomeNoSlot}, "", false, false},
		{"paused", RunResult{Outcome: OutcomePaused}, "", false, false},
		{"skipped", RunResult{Outcome: OutcomeSkipped}, "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			class, failing, ok := classOf(c.res)
			if class != c.class || failing != c.failing || ok != c.ok {
				t.Errorf("got %q %v %v", class, failing, ok)
			}
		})
	}
}

func TestStreakSanitise(t *testing.T) {
	got := sanitiseClass("bad\nrule<!-- x -->@user" + strings.Repeat("a", 100))
	if strings.ContainsAny(got, "\n<>@ !") || len(got) > maxClassLen {
		t.Errorf("class = %q", got)
	}
	if sanitiseClass("\n@") != "scan_failed" {
		t.Error("empty class not defaulted")
	}
}
