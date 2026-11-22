package reaper

import (
	"context"
	"encoding/json"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
)

// LimitRecorder is the part of *limits.Book the Finisher uses.
type LimitRecorder interface {
	// Exhaust records that family ("" is the whole account) is out of quota
	// and returns the time it stays blocked until. It is idempotent.
	Exhaust(ctx context.Context, family string, resetsAt time.Time) (time.Time, error)
	// Succeeded resets the no-reset backoff after a job ended successfully.
	Succeeded(ctx context.Context, family string) error
}

// UsageLimitClassifier returns the Finisher.Classify hook for a usage limit.
// It is pure.
//
// A run that succeeded, or that held no limit signal, has no usage-limit
// cause. Only a subscription (OAuth token) has usage limits: with an API key a
// bare rate_limit signal is a 429 throttle, which stays the transient
// rate_limit retry of CauseOf. Retry is always false: a limit stop neither
// uses up nor grants the one automatic retry. Detail is fixed text built from
// enums; nothing the CLI printed is echoed.
func UsageLimitClassifier(auth agent.Auth) func(res agent.Result) (closeout.Cause, bool) {
	return func(res agent.Result) (closeout.Cause, bool) {
		hit := res.LimitHit
		if res.OK || hit == nil {
			return closeout.Cause{}, false
		}
		if auth != agent.AuthOAuthToken && hit.Signal == agent.SignalRateLimitError {
			return closeout.Cause{}, false
		}
		return closeout.Cause{Kind: closeout.CauseUsageLimit, Detail: limitDetail(hit), Transient: true, Retry: false,
			Family: hit.Family, ResetsAt: hit.ResetsAt}, true
	}
}

func limitDetail(h *agent.LimitHit) string {
	switch h.Scope {
	case agent.ScopeSession:
		return "session limit"
	case agent.ScopeWeekly:
		return "weekly limit"
	case agent.ScopeModel:
		switch h.Family {
		case "opus":
			return "opus limit"
		case "sonnet":
			return "sonnet limit"
		}
	}
	return "usage limit"
}

// recordedLimit reads the usage-limit cause back out of a stored close-out
// record. ok is false when the record has none.
func recordedLimit(plan json.RawMessage) (family string, resetsAt time.Time, ok bool) {
	var rec struct {
		Job struct {
			Cause struct {
				Kind     closeout.CauseKind
				Family   string
				ResetsAt time.Time
			}
		}
	}
	if json.Unmarshal(plan, &rec) != nil || rec.Job.Cause.Kind != closeout.CauseUsageLimit {
		return "", time.Time{}, false
	}
	return rec.Job.Cause.Family, rec.Job.Cause.ResetsAt, true
}
