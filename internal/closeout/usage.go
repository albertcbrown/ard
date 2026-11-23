package closeout

import (
	"encoding/json"
	"fmt"
	"time"
)

// usageSchema is the version of the tracking file format.
const usageSchema = 1

// usageFile is .ard/<issue#>/costs/<job-id>.json. The controller writes it, one
// per job, so each file has exactly one writer. It holds no free text: every
// string is a job id, a stage or role from config, a fixed outcome or cause
// word, a validated model id or an RFC 3339 time.
type usageFile struct {
	Schema    int         `json:"schema"`
	Job       string      `json:"job"`
	Issue     int         `json:"issue"`
	PR        int         `json:"pr,omitempty"`
	Stage     string      `json:"stage"`
	Role      string      `json:"role"`
	Attempt   int         `json:"attempt"`
	Outcome   string      `json:"outcome"`
	Cause     string      `json:"cause,omitempty"`
	Models    []string    `json:"models"`
	Tokens    *usageToken `json:"tokens"`
	CostUSD   *float64    `json:"cost_usd"`
	Steps     int         `json:"steps"`
	StartedAt *string     `json:"started_at"`
	EndedAt   string      `json:"ended_at"`
}

type usageToken struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheWrite int64 `json:"cache_write"`
	CacheRead  int64 `json:"cache_read"`
}

// usagePath is the repo path of a job's tracking file.
func usagePath(n int, jobID string) string {
	return fmt.Sprintf(".ard/%d/costs/%s.json", n, jobID)
}

// stageUsage stages the job's tracking file. It is controller-written and is
// never checked against the agent allowlist (WritableFor): an agent write to
// this path is denied like any other path outside the allowlist.
func stageUsage(stage string, j Job, n int, plan Plan, now time.Time) (FileRef, error) {
	f := usageFile{Schema: usageSchema, Job: j.ID, Issue: n, PR: j.PR, Stage: j.Stage, Role: j.Role,
		Attempt: j.Attempt, Outcome: string(plan.Outcome), Cause: string(j.Cause.Kind),
		Models: j.Usage.Models, CostUSD: j.Usage.CostUSD, Steps: j.Usage.Steps,
		EndedAt: now.UTC().Format(time.RFC3339)}
	if f.Models == nil {
		f.Models = []string{}
	}
	if t := j.Usage.Tokens; t != nil {
		f.Tokens = &usageToken{t.Input, t.Output, t.CacheWrite, t.CacheRead}
	}
	if !j.StartedAt.IsZero() {
		s := j.StartedAt.UTC().Format(time.RFC3339)
		f.StartedAt = &s
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return FileRef{}, fmt.Errorf("closeout job %s: usage file: %w", j.ID, err)
	}
	ref, err := stageFile(stage, usagePath(n, j.ID), append(b, '\n'))
	if err != nil {
		return FileRef{}, fmt.Errorf("closeout job %s: %w", j.ID, err)
	}
	return ref, nil
}
