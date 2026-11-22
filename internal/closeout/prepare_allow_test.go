package closeout

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestPrepareWriteAllowlist runs jobs that write outside (or inside) their
// allowlist against real git.
func TestPrepareWriteAllowlist(t *testing.T) {
	const basePlan = "# Plan for #7\n\n## Design\n\nthe design\n"
	type tc struct {
		name        string
		stage, role string
		setup       func(e *env, j *Job)
		outcome     Outcome
		files       []string // committed paths besides the log, sorted
		reason      string
	}
	rf := func(stage, role string) string {
		if stage == StageReview {
			stage = "review"
		}
		return ".ard/7/" + stage + "-" + role + ".md"
	}
	// commitBase writes files and commits them as the job's base.
	commitBase := func(e *env, j *Job, files map[string]string) {
		for p, b := range files {
			e.write(p, b)
		}
		j.BaseSHA = e.commitAll("base")
	}
	engineerDone := func(e *env) { e.write(rf(StageTriage, "engineer"), roleFile("engineer", "done")) }
	tests := []tc{
		{name: "worker rewrites plan.md", stage: StageWorker, role: "engineer",
			setup: func(e *env, j *Job) {
				commitBase(e, j, map[string]string{".ard/7/plan.md": basePlan})
				e.write(rf(StageWorker, "engineer"), roleFile("engineer", "done"))
				e.write(".ard/7/plan.md", "rewritten\n")
			},
			outcome: OutcomeNeedsHuman, reason: ".ard/7/plan.md (not writable by worker:engineer)"},
		{name: "worker writes a triage role file", stage: StageWorker, role: "engineer",
			setup: func(e *env, j *Job) {
				e.write(rf(StageWorker, "engineer"), roleFile("engineer", "done"))
				e.write(rf(StageTriage, "lead"), roleFile("lead", "done"))
			},
			outcome: OutcomeNeedsHuman, reason: ".ard/7/triage-lead.md"},
		{name: "worker deletes issue.md", stage: StageWorker, role: "engineer",
			setup: func(e *env, j *Job) {
				e.write(rf(StageWorker, "engineer"), roleFile("engineer", "done"))
				if err := os.Remove(filepath.Join(e.repo, ".ard/7/issue.md")); err != nil {
					t.Fatal(err)
				}
			},
			outcome: OutcomeNeedsHuman, reason: ".ard/7/issue.md"},
		{name: "reviewer writes a worker file", stage: StageReview, role: "eng-review",
			setup: func(e *env, j *Job) {
				e.write(".ard/7/review-eng-review.md", roleFile("eng-review", "done", "verdict: approve"))
				e.write(rf(StageWorker, "engineer"), roleFile("engineer", "done"))
			},
			outcome: OutcomeNeedsHuman, reason: ".ard/7/worker-engineer.md"},
		{name: "lead writes plan.md", stage: StageTriage, role: "lead",
			setup: func(e *env, j *Job) {
				e.write(rf(StageTriage, "lead"), roleFile("lead", "done"))
				e.write(".ard/7/plan.md", "## Design\nx\n")
			},
			outcome: OutcomeNeedsHuman, reason: ".ard/7/plan.md (not writable by triage:lead)"},
		{name: "lead writes issue.md", stage: StageTriage, role: "lead",
			setup: func(e *env, j *Job) {
				e.write(rf(StageTriage, "lead"), roleFile("lead", "done"))
				e.write(".ard/7/issue.md", "the lead's rewrite")
			},
			outcome: OutcomeNeedsHuman, reason: ".ard/7/issue.md"},
		{name: "engineer appends its section", stage: StageTriage, role: "engineer",
			setup: func(e *env, j *Job) {
				commitBase(e, j, map[string]string{".ard/7/plan.md": basePlan})
				engineerDone(e)
				e.write(".ard/7/plan.md", basePlan+"\n## Implementation\n\nsteps\n")
			},
			outcome: OutcomeDone, files: []string{".ard/7/plan.md", ".ard/7/triage-engineer.md"}},
		{name: "engineer creates plan.md", stage: StageTriage, role: "engineer",
			setup: func(e *env, j *Job) {
				engineerDone(e)
				e.write(".ard/7/plan.md", "# Plan for #7\n\n## Implementation\n\nsteps\n")
			},
			outcome: OutcomeDone, files: []string{".ard/7/plan.md", ".ard/7/triage-engineer.md"}},
		{name: "engineer edits Design", stage: StageTriage, role: "engineer",
			setup: func(e *env, j *Job) {
				commitBase(e, j, map[string]string{".ard/7/plan.md": basePlan})
				engineerDone(e)
				e.write(".ard/7/plan.md", strings.Replace(basePlan, "the design", "my design", 1))
			},
			outcome: OutcomeNeedsHuman, reason: "changes section ## Design"},
		{name: "engineer deletes plan.md", stage: StageTriage, role: "engineer",
			setup: func(e *env, j *Job) {
				commitBase(e, j, map[string]string{".ard/7/plan.md": basePlan})
				engineerDone(e)
				if err := os.Remove(filepath.Join(e.repo, ".ard/7/plan.md")); err != nil {
					t.Fatal(err)
				}
			},
			outcome: OutcomeNeedsHuman, reason: ".ard/7/plan.md (deleted)"},
		{name: "engineer writes issue.md", stage: StageTriage, role: "engineer",
			setup: func(e *env, j *Job) {
				engineerDone(e)
				e.write(".ard/7/issue.md", "rewrite")
			},
			outcome: OutcomeNeedsHuman, reason: ".ard/7/issue.md (not writable by triage:engineer)"},
		{name: "designer: files under design/, deletion carried, UX section", stage: StageTriage, role: "designer",
			setup: func(e *env, j *Job) {
				commitBase(e, j, map[string]string{".ard/7/plan.md": basePlan, ".ard/7/design/old.svg": "<svg/>"})
				e.write(rf(StageTriage, "designer"), roleFile("designer", "done"))
				e.write(".ard/7/design/mock.svg", "<svg/>")
				if err := os.Remove(filepath.Join(e.repo, ".ard/7/design/old.svg")); err != nil {
					t.Fatal(err)
				}
				e.write(".ard/7/plan.md", basePlan+"\n## UX\n\nflow\n")
			},
			outcome: OutcomeDone,
			files:   []string{".ard/7/design/mock.svg", ".ard/7/design/old.svg (delete)", ".ard/7/plan.md", ".ard/7/triage-designer.md"}},
		{name: "engineer writes under design/", stage: StageTriage, role: "engineer",
			setup: func(e *env, j *Job) {
				engineerDone(e)
				e.write(".ard/7/design/mock.svg", "<svg/>")
			},
			outcome: OutcomeNeedsHuman, reason: ".ard/7/design/mock.svg"},
		{name: "four violations list three", stage: StageWorker, role: "engineer",
			setup: func(e *env, j *Job) {
				e.write(rf(StageWorker, "engineer"), roleFile("engineer", "done"))
				for _, n := range []string{"a", "b", "c", "d"} {
					e.write(".ard/7/"+n+".md", "x")
				}
			},
			outcome: OutcomeNeedsHuman, reason: "write not allowed: .ard/7/a.md (not writable by worker:engineer); .ard/7/b.md (not writable by worker:engineer); .ard/7/c.md (not writable by worker:engineer); and 1 more"},
		{name: "missing role file stays failed", stage: StageWorker, role: "engineer",
			setup:   func(e *env, j *Job) { e.write(".ard/7/plan.md", "x") },
			outcome: OutcomeFailed, reason: "role file missing"},
		{name: "secret in a denied file is neither staged nor quoted", stage: StageWorker, role: "engineer",
			setup: func(e *env, j *Job) {
				e.write(rf(StageWorker, "engineer"), roleFile("engineer", "done"))
				e.write(".ard/7/plan.md", "leak "+planted())
			},
			outcome: OutcomeNeedsHuman, reason: "write not allowed: .ard/7/plan.md"},
		{name: "logs/ is ignored, not denied", stage: StageWorker, role: "engineer",
			setup: func(e *env, j *Job) {
				e.write(rf(StageWorker, "engineer"), roleFile("engineer", "done"))
				e.write(".ard/7/logs/x.log", "x")
			},
			outcome: OutcomeDone, files: []string{".ard/7/worker-engineer.md"}},
		{name: "base unreadable fails closed", stage: StageTriage, role: "engineer",
			setup: func(e *env, j *Job) {
				engineerDone(e)
				e.write(".ard/7/plan.md", "## Implementation\nx\n")
				e.d.BaseFile = func(context.Context, string, string, string, int64) ([]byte, bool, error) {
					return nil, false, errors.New("boom")
				}
			},
			outcome: OutcomeNeedsHuman, reason: "could not read it at base: boom"},
		{name: "dry-run issue uses the positive number", stage: StageTriage, role: "engineer",
			setup: func(e *env, j *Job) {
				j.Issue = -7
				engineerDone(e)
				e.write(".ard/7/plan.md", "## Implementation\nx\n")
			},
			outcome: OutcomeDone, files: []string{".ard/7/plan.md", ".ard/7/triage-engineer.md"}},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			j := e.claim(c.stage, c.role)
			if c.stage == StageWorker {
				j.PR, j.Worker = 5, &WorkerResult{PR: 5, Tests: TestsNone}
			}
			c.setup(e, &j)
			rec, err := Prepare(context.Background(), j, e.d)
			if err != nil {
				t.Fatal(err)
			}
			p := rec.Plan
			if p.Outcome != c.outcome {
				t.Errorf("outcome = %s (%s), want %s", p.Outcome, p.Reason, c.outcome)
			}
			want := append([]string(nil), c.files...)
			want = append(want, ".ard/7/logs/"+filepath.Base(j.LogPath))
			if got := paths(p); !sameSet(got, want) {
				t.Errorf("files = %v, want %v", got, want)
			}
			if c.reason != "" && !strings.Contains(p.Reason, c.reason) {
				t.Errorf("reason = %q, want it to contain %q", p.Reason, c.reason)
			}
			if c.outcome == OutcomeNeedsHuman {
				if len(p.Terminal) != 1 || p.Terminal[0].Name != "ard:needs-human" {
					t.Errorf("terminal = %+v", p.Terminal)
				}
			}
			if strings.Contains(p.Reason, planted()) {
				t.Error("reason leaks the secret")
			}
		})
	}
}

func sameSet(a, b []string) bool {
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	return strings.Join(x, "|") == strings.Join(y, "|")
}
