package limits

import (
	"fmt"
	"time"
)

// Reading window names as the Claude stream reports them.
const (
	WindowFiveHour = "five_hour"
	WindowSevenDay = "seven_day"
)

// weekLen is the length of the weekly window; a day is one 24h step of it.
const (
	weekLen = 7 * 24 * time.Hour
	dayLen  = 24 * time.Hour
)

// Tier says which thresholds apply to a job.
type Tier int

const (
	// TierWorker covers workers, triage and agent scanners: they stop at the
	// stop-workers values and at the stop-all values.
	TierWorker Tier = iota
	// TierReviewer covers reviewers: they stop only at the stop-all values.
	TierReviewer
)

// Policy holds the thresholds (#63), in percent. It mirrors config.Limits;
// the caller converts, so this package does not import config.
type Policy struct {
	StopWorkersPct   int    // 5-hour window
	StopAllPct       int    // 5-hour window
	WeeklyByDay      [7]int // weekly stop-workers percent for day 1..7 of the window
	WeeklyStopAllPct int
}

// GateResult is the answer of Book.Gate for one agent job.
type GateResult struct {
	// Deny is true when no new job of the tier may start.
	Deny bool
	// Until is when to look again (a window reset or the next day of the
	// weekly window); zero when unknown. Set only with Deny.
	Until time.Time
	// Serial is true when at most one agent job may run: the 5-hour usage is
	// at or above StopWorkersPct. The weekly window never sets it.
	Serial bool
	// Reason names the window and threshold that decided; empty when allowed.
	Reason string
}

// Percent normalises a utilization as the stream reports it: a value of at
// most 1 is a fraction, anything above is already a percent.
func Percent(u float64) float64 {
	if u <= 1 {
		return u * 100
	}
	return u
}

// WeeklyDay returns the day (1..7) of the weekly window at now, counted from
// resetsAt minus 7 days. A zero resetsAt gives day 7. A time before the window
// start is day 1, after its end day 7.
func WeeklyDay(resetsAt, now time.Time) int {
	if resetsAt.IsZero() {
		return 7
	}
	d := int(now.Sub(resetsAt.Add(-weekLen))/dayLen) + 1
	return min(7, max(1, d))
}

// Gate decides whether a new agent job of tier may start, from the latest
// readings. A reading whose reset time has passed is stale and ignored. The
// reactive per-family pauses (Until) are a separate check.
func (b *Book) Gate(p Policy, tier Tier) GateResult {
	now := b.now()
	var five, week *Reading
	for _, r := range b.Readings() {
		r := r
		if !r.ResetsAt.IsZero() && !r.ResetsAt.After(now) {
			continue
		}
		switch r.Window {
		case WindowFiveHour:
			five = &r
		case WindowSevenDay:
			week = &r
		}
	}
	var res GateResult
	deny := func(until time.Time, format string, args ...any) {
		if !res.Deny {
			res.Deny, res.Until, res.Reason = true, until, fmt.Sprintf(format, args...)
		}
	}
	if five != nil {
		u := Percent(five.Utilization)
		switch {
		case u >= float64(p.StopAllPct):
			deny(five.ResetsAt, "5-hour usage %.0f%% is at the stop-all threshold %d%%", u, p.StopAllPct)
		case u >= float64(p.StopWorkersPct):
			res.Serial = true
			if tier == TierWorker {
				deny(five.ResetsAt, "5-hour usage %.0f%% is at the stop-workers threshold %d%%", u, p.StopWorkersPct)
			}
		}
	}
	if week != nil {
		u := Percent(week.Utilization)
		day := WeeklyDay(week.ResetsAt, now)
		switch {
		case u >= float64(p.WeeklyStopAllPct):
			deny(week.ResetsAt, "weekly usage %.0f%% is at the stop-all threshold %d%%", u, p.WeeklyStopAllPct)
		case tier == TierWorker && u >= float64(p.WeeklyByDay[day-1]):
			until := week.ResetsAt
			if !until.IsZero() && day < 7 {
				until = until.Add(-weekLen).Add(time.Duration(day) * dayLen)
			}
			deny(until, "weekly usage %.0f%% is at the day-%d threshold %d%%", u, day, p.WeeklyByDay[day-1])
		}
	}
	return res
}

// Now returns the book's clock, so callers measure a Gate time against the
// same clock.
func (b *Book) Now() time.Time { return b.now() }
