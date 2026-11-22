package closeout

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/github"
)

func TestBuildWorker(t *testing.T) {
	base := Job{Stage: StageWorker, Role: "engineer", Issue: 7, PR: 9}
	with := func(w *WorkerResult, pr int) Job {
		j := base
		j.Worker, j.PR = w, pr
		return j
	}
	done := RoleFile{Status: "done", Next: []string{"review:license-review", "review:eng-review"}}
	tests := []struct {
		name      string
		j         Job
		rf        RoleFile
		outcome   Outcome
		terminal  []string
		remove    []string
		requests  []string
		skipped   int
		reasonHas string
	}{
		{"no result", with(nil, 9), done, OutcomeNeedsHuman, []string{"7:ard:needs-human"}, []string{"7:ard:worker:engineer:running"}, nil, 0, "without the loop's result"},
		{"hold", with(&WorkerResult{Hold: "diff guard: x"}, 9), done, OutcomeNeedsHuman, []string{"7:ard:needs-human"}, []string{"7:ard:worker:engineer:running"}, nil, 0, "diff guard: x"},
		{"no PR", with(&WorkerResult{Tests: TestsPassed}, 0), done, OutcomeNeedsHuman, []string{"7:ard:needs-human"}, []string{"7:ard:worker:engineer:running"}, nil, 0, "without a pull request"},
		{"needs-human status", with(&WorkerResult{Tests: TestsPassed}, 9), RoleFile{Status: "needs-human"}, OutcomeNeedsHuman, []string{"7:ard:needs-human"}, []string{"7:ard:worker:engineer:running"}, nil, 0, ""},
		{"tests passed: reviewers then next, no duplicates", with(&WorkerResult{Tests: TestsPassed, Reviewers: []string{"eng-review", "security-review", "ghost"}}, 9), done,
			OutcomeDone, []string{"7:ard:worker:engineer:done"},
			[]string{"7:ard:tests:failed", "9:ard:tests:failed", "7:ard:worker:engineer:running"},
			[]string{"9:ard:review:eng-review", "9:ard:review:security-review", "9:ard:review:license-review"}, 1, ""},
		{"tests failed: no reviewers, labels on both", with(&WorkerResult{Tests: TestsFailed, Reviewers: []string{"eng-review"}}, 9), done,
			OutcomeDone, []string{"7:ard:worker:engineer:done", "7:ard:tests:failed", "9:ard:tests:failed"},
			[]string{"7:ard:worker:engineer:running"}, nil, 2, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := Build(tc.j, tc.rf, testRoles)
			if p.Outcome != tc.outcome {
				t.Errorf("outcome = %s (%s), want %s", p.Outcome, p.Reason, tc.outcome)
			}
			if got := lbls(p.Terminal); !reflect.DeepEqual(got, tc.terminal) {
				t.Errorf("terminal = %v, want %v", got, tc.terminal)
			}
			if got := lbls(p.Remove); !reflect.DeepEqual(got, tc.remove) {
				t.Errorf("remove = %v, want %v", got, tc.remove)
			}
			if got := lbls(p.Requests); !reflect.DeepEqual(got, tc.requests) {
				t.Errorf("requests = %v, want %v", got, tc.requests)
			}
			if len(p.Skipped) != tc.skipped {
				t.Errorf("skipped = %v, want %d", p.Skipped, tc.skipped)
			}
			if !strings.Contains(p.Reason, tc.reasonHas) {
				t.Errorf("reason = %q, want %q", p.Reason, tc.reasonHas)
			}
		})
	}
}

func TestValidateWorkerBranch(t *testing.T) {
	ok := Job{ID: "a", Stage: StageWorker, Role: "x", Issue: 1, BaseSHA: "abc"}
	if err := ok.validate(); err != nil {
		t.Errorf("worker with no branch: %v", err)
	}
	bad := Job{ID: "a", Stage: StageReview, Role: "x", Issue: 1, PR: 2, BaseSHA: "abc"}
	if err := bad.validate(); err == nil {
		t.Error("review with no branch accepted")
	}
}

func TestWorkerComment(t *testing.T) {
	for name, tc := range map[string]struct {
		w    WorkerResult
		want []string
	}{
		"passed":    {WorkerResult{PR: 9, PRURL: "https://github.com/o/r/pull/9", Tests: TestsPassed}, []string{"PR: [#9](https://github.com/o/r/pull/9)", "Tests: passed\n"}},
		"after fix": {WorkerResult{PR: 9, Tests: TestsPassed, Fixes: 1}, []string{"PR: #9", "Tests: passed after 1 fix attempt"}},
		"failed":    {WorkerResult{PR: 9, Tests: TestsFailed, Fixes: 2, TestNote: "ran out of time"}, []string{"Tests: failed after 2 fix attempts · ran out of time"}},
		"none":      {WorkerResult{PR: 9, Tests: TestsNone}, []string{"Tests: not configured"}},
		"bad url":   {WorkerResult{PR: 9, PRURL: "https://evil.example/x", Tests: TestsNone}, []string{"PR: #9\n"}},
	} {
		t.Run(name, func(t *testing.T) {
			w := tc.w
			r := Record{Job: Job{ID: "J1", Stage: StageWorker, Role: "engineer", Issue: 7, PR: 9, Worker: &w}, Plan: Plan{Outcome: OutcomeDone}}
			got := RenderComment(r, github.Repo{Owner: "o", Name: "r"}, true)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("comment lacks %q:\n%s", want, got)
				}
			}
		})
	}
	r := Record{Job: Job{ID: "J1", Stage: StageWorker, Role: "engineer", Issue: 7, PR: 9, Worker: &WorkerResult{Hold: "x", Tests: TestsPassed}}, Plan: Plan{Outcome: OutcomeNeedsHuman}}
	if got := RenderComment(r, github.Repo{}, true); strings.Contains(got, "Tests:") {
		t.Errorf("a hold must not print tests:\n%s", got)
	}
}

func TestPrepareExtraLogs(t *testing.T) {
	newJob := func(e *env) (Job, string, string) {
		j := e.claim(StageWorker, "engineer")
		j.PR, j.Worker = 9, &WorkerResult{PR: 9, Tests: TestsPassed}
		e.write(".ard/7/worker-engineer.md", roleFile("engineer", "done"))
		clean := filepath.Join(e.logDir, "worker-engineer-20260102T030300Z.log")
		dirty := filepath.Join(e.logDir, "worker-engineer-20260102T030301Z.log")
		if err := os.WriteFile(clean, []byte("first step\nkey="+secretValue+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dirty, []byte("leak "+planted()+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return j, clean, dirty
	}
	t.Run("staged and redacted", func(t *testing.T) {
		e := newEnv(t)
		j, clean, _ := newJob(e)
		j.ExtraLogs = []string{clean}
		rec, err := Prepare(context.Background(), j, e.d)
		if err != nil {
			t.Fatal(err)
		}
		p := rec.Plan
		want := ".ard/7/logs/worker-engineer-20260102T030300Z.log"
		if p.Outcome != OutcomeDone || !reflect.DeepEqual(p.ExtraLogPaths, []string{want}) {
			t.Fatalf("plan = %+v", p)
		}
		for _, f := range p.Files {
			if f.Path == want {
				b, _ := os.ReadFile(f.Staged)
				if strings.Contains(string(b), secretValue) {
					t.Error("extra log not redacted")
				}
				return
			}
		}
		t.Errorf("extra log not in files: %v", paths(p))
	})
	t.Run("flagged extra log holds the job", func(t *testing.T) {
		e := newEnv(t)
		j, _, dirty := newJob(e)
		j.ExtraLogs = []string{dirty}
		rec, err := Prepare(context.Background(), j, e.d)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Plan.Outcome != OutcomeNeedsHuman || !rec.Plan.LogWithheld || len(rec.Plan.ExtraLogPaths) != 0 {
			t.Errorf("plan = %+v", rec.Plan)
		}
	})
}
