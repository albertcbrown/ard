package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Limits holds the subscription-limit thresholds (#63). Percents are of the
// usage window as the stream reports it.
type Limits struct {
	// StopWorkersPct: at this 5-hour usage no new worker, triage or agent
	// scanner starts and at most one agent job runs. Default 75.
	StopWorkersPct int
	// StopAllPct: at this 5-hour usage no new agent job starts. Default 90.
	StopAllPct int
	// WeeklyStopWorkersPctByDay is one percent per day of the 7-day window;
	// on day N new workers, triage and agent scanners stop at the N-th value.
	WeeklyStopWorkersPctByDay []int
	// WeeklyStopAllPct: at this weekly usage no new agent job starts.
	WeeklyStopAllPct int
}

const (
	defaultStopWorkersPct   = 75
	defaultStopAllPct       = 90
	defaultWeeklyStopAllPct = 90
)

func defaultWeeklyByDay() []int { return []int{20, 35, 50, 60, 70, 80, 85} }

type rawLimits struct {
	stopWorkersPct, stopAllPct, weeklyStopAllPct *int
	weeklyByDay                                  []int
	haveByDay                                    bool
}

func (rl *rawLimits) parse(n *yaml.Node, file string) Errors {
	var errs Errors
	if n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return Errors{{File: file, Key: "limits", Msg: "must be a mapping"}}
	}
	for i := 0; i < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		k := "limits." + key.Value
		switch key.Value {
		case "stop_workers_pct":
			errs = append(errs, decodeValue(value, file, k, &rl.stopWorkersPct)...)
		case "stop_all_pct":
			errs = append(errs, decodeValue(value, file, k, &rl.stopAllPct)...)
		case "weekly_stop_all_pct":
			errs = append(errs, decodeValue(value, file, k, &rl.weeklyStopAllPct)...)
		case "weekly_stop_workers_pct_by_day":
			if value.Tag == "!!null" {
				continue
			}
			var list []int
			ok := value.Kind == yaml.SequenceNode
			for _, item := range value.Content {
				if item.Kind != yaml.ScalarNode || item.Tag != "!!int" {
					ok = false
				}
			}
			if ok {
				ok = value.Decode(&list) == nil
			}
			if !ok {
				errs = append(errs, &Error{File: file, Key: k, Msg: "must be a list of 7 integers"})
				continue
			}
			rl.weeklyByDay, rl.haveByDay = list, true
		default:
			errs = append(errs, &Error{File: file, Key: k, Msg: "unknown key (want stop_workers_pct, stop_all_pct, weekly_stop_workers_pct_by_day or weekly_stop_all_pct)"})
		}
	}
	return errs
}

func applyLimits(res *Resolved, raw rawLimits, file string) Errors {
	var errs Errors
	l := Limits{StopWorkersPct: defaultStopWorkersPct, StopAllPct: defaultStopAllPct,
		WeeklyStopWorkersPctByDay: defaultWeeklyByDay(), WeeklyStopAllPct: defaultWeeklyStopAllPct}
	if raw.stopWorkersPct != nil {
		l.StopWorkersPct = *raw.stopWorkersPct
	}
	if raw.stopAllPct != nil {
		l.StopAllPct = *raw.stopAllPct
	}
	if raw.weeklyStopAllPct != nil {
		l.WeeklyStopAllPct = *raw.weeklyStopAllPct
	}
	if raw.haveByDay {
		l.WeeklyStopWorkersPctByDay = raw.weeklyByDay
	}
	bad := func(key, msg string) { errs = append(errs, &Error{File: file, Key: "limits." + key, Msg: msg}) }
	inRange := func(key string, v int) bool {
		if v < 1 || v > 100 {
			bad(key, fmt.Sprintf("must be between 1 and 100, got %d", v))
			return false
		}
		return true
	}
	a := inRange("stop_workers_pct", l.StopWorkersPct)
	b := inRange("stop_all_pct", l.StopAllPct)
	if a && b && l.StopWorkersPct > l.StopAllPct {
		bad("stop_workers_pct", fmt.Sprintf("must be at most stop_all_pct (%d), got %d", l.StopAllPct, l.StopWorkersPct))
	}
	w := inRange("weekly_stop_all_pct", l.WeeklyStopAllPct)
	const key = "weekly_stop_workers_pct_by_day"
	if len(l.WeeklyStopWorkersPctByDay) != 7 {
		bad(key, fmt.Sprintf("must have exactly 7 values, got %d", len(l.WeeklyStopWorkersPctByDay)))
	} else {
		for i, v := range l.WeeklyStopWorkersPctByDay {
			ik := fmt.Sprintf("%s[%d]", key, i)
			if !inRange(ik, v) {
				continue
			}
			if i > 0 && v < l.WeeklyStopWorkersPctByDay[i-1] {
				bad(ik, fmt.Sprintf("must not be less than the previous day (%d), got %d", l.WeeklyStopWorkersPctByDay[i-1], v))
			}
			if w && v > l.WeeklyStopAllPct {
				bad(ik, fmt.Sprintf("must be at most weekly_stop_all_pct (%d), got %d", l.WeeklyStopAllPct, v))
			}
		}
	}
	res.Limits = l
	return errs
}
