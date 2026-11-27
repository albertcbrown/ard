package limits

import (
	"context"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
)

var defaultPolicy = Policy{StopWorkersPct: 75, StopAllPct: 90, WeeklyByDay: [7]int{20, 35, 50, 60, 70, 80, 85}, WeeklyStopAllPct: 90}

// gateAt observes a 5-hour and a weekly reading and gates at now.
func gateAt(t *testing.T, p Policy, tier Tier, now time.Time, five, week float64, weekResets time.Time) GateResult {
	t.Helper()
	c := &clock{now}
	b, _ := open(t, &fakeMeta{}, c)
	ws := []agent.Window{
		{Type: "five_hour", Utilization: five, ResetsAt: now.Add(3 * time.Hour)},
		{Type: "seven_day", Utilization: week, ResetsAt: weekResets},
	}
	if err := b.Observe(context.Background(), ws); err != nil {
		t.Fatal(err)
	}
	return b.Gate(p, tier)
}

func TestGateFiveHour(t *testing.T) {
	tests := []struct {
		pct          float64
		tier         Tier
		deny, serial bool
	}{
		{74, TierWorker, false, false},
		{75, TierWorker, true, true},
		{75, TierReviewer, false, true},
		{89, TierReviewer, false, true},
		{90, TierWorker, true, false},
		{90, TierReviewer, true, false},
		{0.74, TierWorker, false, false}, // fraction form
		{0.75, TierWorker, true, true},
		{0.9, TierReviewer, true, false},
	}
	for _, tc := range tests {
		got := gateAt(t, defaultPolicy, tc.tier, t0, tc.pct, 0, t0.Add(6*dayLen))
		if got.Deny != tc.deny || got.Serial != tc.serial {
			t.Errorf("%v tier %d: %+v, want deny=%v serial=%v", tc.pct, tc.tier, got, tc.deny, tc.serial)
		}
		if tc.deny && !got.Until.Equal(t0.Add(3*time.Hour)) {
			t.Errorf("%v: Until = %v", tc.pct, got.Until)
		}
	}
}

func TestGateWeeklyPaced(t *testing.T) {
	start := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	resets := start.Add(weekLen)
	tests := []struct {
		name string
		now  time.Time
		pct  float64
		tier Tier
		deny bool
	}{
		{"day 1 at 19", start.Add(2 * time.Hour), 19, TierWorker, false},
		{"day 1 at 20", start.Add(2 * time.Hour), 20, TierWorker, true},
		{"day 1 at 20 reviewer", start.Add(2 * time.Hour), 20, TierReviewer, false},
		{"exactly start+24h is day 2", start.Add(dayLen), 20, TierWorker, false},
		{"day 2 at 35", start.Add(dayLen), 35, TierWorker, true},
		{"day 6 at 74", start.Add(5*dayLen + time.Hour), 74, TierWorker, false},
		{"day 6 at 80", start.Add(5*dayLen + time.Hour), 80, TierWorker, true},
		{"day 7 at 84", start.Add(6*dayLen + time.Hour), 84, TierWorker, false},
		{"day 7 at 85", start.Add(6*dayLen + time.Hour), 85, TierWorker, true},
		{"fraction", start.Add(2 * time.Hour), 0.2, TierWorker, true},
	}
	for _, tc := range tests {
		if got := gateAt(t, defaultPolicy, tc.tier, tc.now, 0, tc.pct, resets); got.Deny != tc.deny || got.Serial {
			t.Errorf("%s: %+v, want deny=%v, no serial", tc.name, got, tc.deny)
		}
	}
	for d := 0; d < 7; d++ {
		for _, tier := range []Tier{TierWorker, TierReviewer} {
			if !gateAt(t, defaultPolicy, tier, start.Add(time.Duration(d)*dayLen+time.Hour), 0, 90, resets).Deny {
				t.Errorf("day %d tier %d: 90%% weekly must deny", d+1, tier)
			}
		}
	}
	// A day-1 pacing denial looks again at the start of day 2.
	if got := gateAt(t, defaultPolicy, TierWorker, start.Add(time.Hour), 0, 25, resets); !got.Until.Equal(start.Add(dayLen)) {
		t.Errorf("Until = %v", got.Until)
	}
}

func TestGateWeeklyCustomAndNoReset(t *testing.T) {
	custom := defaultPolicy
	custom.WeeklyByDay = [7]int{10, 10, 10, 10, 10, 10, 10}
	start := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	if !gateAt(t, custom, TierWorker, start.Add(time.Hour), 0, 10, start.Add(weekLen)).Deny {
		t.Error("custom list ignored")
	}
	// No resets_at: day 7 (85).
	if gateAt(t, defaultPolicy, TierWorker, t0, 0, 84, time.Time{}).Deny {
		t.Error("84% with no resets_at must pass day 7")
	}
	if !gateAt(t, defaultPolicy, TierWorker, t0, 0, 85, time.Time{}).Deny {
		t.Error("85% with no resets_at must stop")
	}
}

func TestGateIgnoresStaleReadings(t *testing.T) {
	c := &clock{t0}
	b, _ := open(t, &fakeMeta{}, c)
	if err := b.Observe(context.Background(), []agent.Window{
		{Type: "five_hour", Utilization: 95, ResetsAt: t0.Add(time.Hour)},
		{Type: "seven_day", Utilization: 95, ResetsAt: t0.Add(2 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	if !b.Gate(defaultPolicy, TierReviewer).Deny {
		t.Fatal("fresh readings must deny")
	}
	c.advance(3 * time.Hour)
	if got := b.Gate(defaultPolicy, TierWorker); got.Deny || got.Serial {
		t.Errorf("rolled-over windows still gate: %+v", got)
	}
	// No readings at all: allowed.
	b2, _ := open(t, &fakeMeta{}, c)
	if b2.Gate(defaultPolicy, TierWorker).Deny {
		t.Error("empty book denied")
	}
}

func TestWeeklyDayClamps(t *testing.T) {
	r := t0.Add(weekLen)
	if d := WeeklyDay(r, t0.Add(-dayLen)); d != 1 {
		t.Errorf("before start = %d", d)
	}
	if d := WeeklyDay(r, r.Add(dayLen)); d != 7 {
		t.Errorf("after end = %d", d)
	}
}
