package closeout

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func costOf(v float64) *float64 { return &v }

func usageOnly(t *testing.T, p Plan) FileRef {
	t.Helper()
	var got []FileRef
	for _, f := range p.Files {
		if strings.Contains(f.Path, "/costs/") {
			got = append(got, f)
		}
	}
	if len(got) != 1 {
		t.Fatalf("usage files = %v, want exactly one", got)
	}
	return got[0]
}

func TestPrepareStagesUsageFileForEveryOutcome(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	usage := Usage{Models: []string{"claude-opus-5-5"}, Tokens: &Tokens{20, 3300, 19100, 173500}, CostUSD: costOf(0.42), Steps: 3}
	for _, tc := range []struct {
		name    string
		stage   string
		role    string
		cause   Cause
		usage   Usage
		files   func(e *env)
		outcome Outcome
		want    string
	}{
		{"done", StageTriage, "lead", Cause{}, usage, func(e *env) { e.write(".ard/7/triage-lead.md", roleFile("lead", "done")) }, OutcomeDone, `{
  "schema": 1,
  "job": "ID",
  "issue": 7,
  "stage": "triage",
  "role": "lead",
  "attempt": 1,
  "outcome": "done",
  "models": [
    "claude-opus-5-5"
  ],
  "tokens": {
    "input": 20,
    "output": 3300,
    "cache_write": 19100,
    "cache_read": 173500
  },
  "cost_usd": 0.42,
  "steps": 3,
  "started_at": "2026-01-02T03:00:00Z",
  "ended_at": "2026-01-02T03:04:05Z"
}
`},
		{"failed", StageWorker, "engineer", Cause{Kind: CauseDeadline, Detail: "ran past its deadline"}, usage, nil, OutcomeFailed, `{
  "schema": 1,
  "job": "ID",
  "issue": 7,
  "stage": "worker",
  "role": "engineer",
  "attempt": 1,
  "outcome": "failed",
  "cause": "deadline",
  "models": [
    "claude-opus-5-5"
  ],
  "tokens": {
    "input": 20,
    "output": 3300,
    "cache_write": 19100,
    "cache_read": 173500
  },
  "cost_usd": 0.42,
  "steps": 3,
  "started_at": "2026-01-02T03:00:00Z",
  "ended_at": "2026-01-02T03:04:05Z"
}
`},
		{"retry, nothing reported", StageTriage, "lead", Cause{Kind: CauseRateLimit, Transient: true, Retry: true}, Usage{}, nil, OutcomeRetry, `{
  "schema": 1,
  "job": "ID",
  "issue": 7,
  "stage": "triage",
  "role": "lead",
  "attempt": 1,
  "outcome": "retry",
  "cause": "rate_limit",
  "models": [],
  "tokens": null,
  "cost_usd": null,
  "steps": 0,
  "started_at": "2026-01-02T03:00:00Z",
  "ended_at": "2026-01-02T03:04:05Z"
}
`},
		{"usage limit", StageTriage, "lead", Cause{Kind: CauseUsageLimit, ResetsAt: started.Add(time.Hour)}, Usage{CostUSD: costOf(1)}, nil, OutcomeLimited, `{
  "schema": 1,
  "job": "ID",
  "issue": 7,
  "stage": "triage",
  "role": "lead",
  "attempt": 1,
  "outcome": "usage-limit",
  "cause": "usage_limit",
  "models": [],
  "tokens": null,
  "cost_usd": 1,
  "steps": 0,
  "started_at": "2026-01-02T03:00:00Z",
  "ended_at": "2026-01-02T03:04:05Z"
}
`},
		{"needs-human (write denied)", StageTriage, "lead", Cause{}, Usage{}, func(e *env) {
			e.write(".ard/7/triage-lead.md", roleFile("lead", "done"))
			e.write(".ard/7/issue.md", "rewritten")
		}, OutcomeNeedsHuman, `{
  "schema": 1,
  "job": "ID",
  "issue": 7,
  "stage": "triage",
  "role": "lead",
  "attempt": 1,
  "outcome": "needs-human",
  "models": [],
  "tokens": null,
  "cost_usd": null,
  "steps": 0,
  "started_at": "2026-01-02T03:00:00Z",
  "ended_at": "2026-01-02T03:04:05Z"
}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			if tc.files != nil {
				tc.files(e)
			}
			j := e.claim(tc.stage, tc.role)
			j.Cause, j.Usage, j.StartedAt = tc.cause, tc.usage, started
			rec, err := Prepare(context.Background(), j, e.d)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Plan.Outcome != tc.outcome {
				t.Fatalf("outcome = %s (%s), want %s", rec.Plan.Outcome, rec.Plan.Reason, tc.outcome)
			}
			f := usageOnly(t, rec.Plan)
			if want := ".ard/7/costs/" + j.ID + ".json"; f.Path != want {
				t.Errorf("path = %s, want %s", f.Path, want)
			}
			b, err := os.ReadFile(f.Staged)
			if err != nil {
				t.Fatal(err)
			}
			if want := strings.ReplaceAll(tc.want, `"ID"`, `"`+j.ID+`"`); string(b) != want {
				t.Errorf("file =\n%s\nwant\n%s", b, want)
			}
		})
	}
}

func TestUsageFileCarriesPR(t *testing.T) {
	e := newEnv(t)
	j := e.claim(StageReview, "eng-review")
	j.Cause = Cause{Kind: CauseDeadline}
	rec, err := Prepare(context.Background(), j, e.d)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(usageOnly(t, rec.Plan).Staged)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m["pr"] != float64(9) {
		t.Errorf("file = %s (%v)", b, err)
	}
	if _, has := m["started_at"]; !has || m["started_at"] != nil {
		t.Errorf("zero StartedAt should be null: %s", b)
	}
}

// An agent that writes its own usage record is denied like any other path
// outside its allowlist, and the committed bytes are the controller's.
func TestAgentCannotForgeUsageFile(t *testing.T) {
	for _, tc := range []struct{ name, stage, role string }{
		{"triage", StageTriage, "lead"}, {"worker", StageWorker, "engineer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			j := e.claim(tc.stage, tc.role)
			j.Usage = Usage{CostUSD: costOf(0.5), Steps: 1}
			e.write(".ard/7/"+tc.stage+"-"+tc.role+".md", roleFile(tc.role, "done"))
			own := ".ard/7/costs/" + j.ID + ".json"
			other := ".ard/7/costs/OTHERJOB.json"
			e.write(own, `{"forged":true,"cost_usd":0}`)
			e.write(other, `{"forged":true}`)
			out, err := New(e.d).Run(context.Background(), j)
			if err != nil || out != OutcomeNeedsHuman {
				t.Fatalf("Run = %s, %v; want needs-human", out, err)
			}
			var committed bool
			for _, files := range e.cm.commits {
				if got, ok := files[own]; ok {
					committed = true
					if strings.Contains(got, "forged") || !strings.Contains(got, `"cost_usd": 0.5`) {
						t.Errorf("committed forged bytes: %s", got)
					}
				}
				if _, ok := files[other]; ok {
					t.Errorf("another job's usage file was committed")
				}
				if _, ok := files[".ard/7/"+tc.stage+"-"+tc.role+".md"]; ok {
					t.Errorf("role file committed on a denied job")
				}
			}
			if !committed {
				t.Error("controller's usage file not committed")
			}
		})
	}
}

func TestUsageFileResumeCommitsSameBytes(t *testing.T) {
	e := newEnv(t)
	e.write(".ard/7/triage-lead.md", roleFile("lead", "done"))
	j := e.claim(StageTriage, "lead")
	j.Usage = Usage{Tokens: &Tokens{1, 2, 3, 4}, CostUSD: costOf(0.1), Steps: 1}
	rec, err := Prepare(context.Background(), j, e.d)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(usageOnly(t, rec.Plan).Staged)
	cl := New(e.d)
	if err := cl.save(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	// Time moves on and the worktree is gone: the staged bytes are reused.
	e.d.Now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	if err := os.RemoveAll(e.repo); err != nil {
		t.Fatal(err)
	}
	if err := New(e.d).Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := ".ard/7/costs/" + j.ID + ".json"
	n := 0
	for _, files := range e.cm.commits {
		if got, ok := files[path]; ok {
			n++
			if got != string(want) {
				t.Errorf("resumed bytes differ:\n%s\nvs\n%s", got, want)
			}
		}
	}
	if n != 1 {
		t.Errorf("usage file committed in %d commits", n)
	}
}

func TestReviewUsageFileGoesToBranch(t *testing.T) {
	e := newEnv(t)
	j := e.claim(StageReview, "eng-review")
	j.Cause = Cause{Kind: CauseDeadline}
	if _, err := New(e.d).Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if len(e.cm.branches) != 1 || e.cm.branches[0] != j.Branch {
		t.Fatalf("branches = %q, want [%s]", e.cm.branches, j.Branch)
	}
	for _, files := range e.cm.commits {
		if _, ok := files[".ard/7/costs/"+j.ID+".json"]; !ok {
			t.Errorf("review commit lacks the usage file: %v", files)
		}
	}
}
