package closeout

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/redact"
)

func paths(p Plan) []string {
	var out []string
	for _, f := range p.Files {
		s := f.Path
		if f.Delete {
			s += " (delete)"
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func staged(t *testing.T, p Plan, path string) string {
	t.Helper()
	for _, f := range p.Files {
		if f.Path == path {
			b, err := os.ReadFile(f.Staged)
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
	}
	t.Fatalf("%s not in %v", path, paths(p))
	return ""
}

const logPath = ".ard/7/logs/triage-lead-20260102T030405Z.log"

func TestPrepare(t *testing.T) {
	type tc struct {
		name    string
		setup   func(e *env, j *Job)
		outcome Outcome
		files   []string
		reason  string // substring of Plan.Reason
		withhld bool
		labels  []string // terminal label names
	}
	good := func(e *env) { e.write(".ard/7/triage-lead.md", roleFile("lead", "done")) }
	tests := []tc{
		{name: "only changed files, deletion carried, logs and other issues ignored",
			setup: func(e *env, j *Job) {
				good(e)
				e.write(".ard/7/plan.md", "plan")
				e.write(".ard/7/logs/agent-wrote-this.log", "ignored")
				e.write(".ard/8/triage-lead.md", "other issue")
				e.write("README.md", "elsewhere")
				if err := os.Remove(filepath.Join(e.repo, ".ard/7/issue.md")); err != nil {
					t.Fatal(err)
				}
			},
			outcome: OutcomeDone, labels: []string{"ard:triage:lead:done"},
			files: []string{".ard/7/issue.md (delete)", ".ard/7/logs/triage-lead-20260102T030405Z.log", ".ard/7/plan.md", ".ard/7/triage-lead.md"}},
		{name: "unchanged files are not sent", setup: func(e *env, j *Job) { good(e) },
			outcome: OutcomeDone, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log", ".ard/7/triage-lead.md"}, labels: []string{"ard:triage:lead:done"}},
		{name: "non-zero exit commits only the log", setup: func(e *env, j *Job) {
			good(e)
			e.write(".ard/7/plan.md", "partial")
			j.ExitCode = 3
		}, outcome: OutcomeFailed, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log"}, reason: "agent exited 3",
			labels: []string{"ard:executor:failed"}},
		{name: "missing role file", setup: func(e *env, j *Job) { e.write(".ard/7/plan.md", "partial") },
			outcome: OutcomeFailed, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log"}, reason: "role file missing",
			labels: []string{"ard:executor:failed"}},
		{name: "malformed role file", setup: func(e *env, j *Job) { e.write(".ard/7/triage-lead.md", "no frontmatter") },
			outcome: OutcomeFailed, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log"}, reason: "malformed role file: no opening",
			labels: []string{"ard:executor:failed"}},
		{name: "role mismatch", setup: func(e *env, j *Job) { e.write(".ard/7/triage-lead.md", roleFile("architect", "done")) },
			outcome: OutcomeFailed, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log"}, reason: "does not match",
			labels: []string{"ard:executor:failed"}},
		{name: "blocked", setup: func(e *env, j *Job) { e.write(".ard/7/triage-lead.md", roleFile("lead", "blocked")) },
			outcome: OutcomeNeedsHuman, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log", ".ard/7/triage-lead.md"},
			labels: []string{"ard:needs-human"}},
		{name: "log flagged: withheld, role file still committed", setup: func(e *env, j *Job) {
			good(e)
			if err := os.WriteFile(j.LogPath, []byte("oops "+planted()+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, outcome: OutcomeNeedsHuman, files: []string{".ard/7/triage-lead.md"}, withhld: true, reason: "log withheld",
			labels: []string{"ard:needs-human"}},
		{name: "plan.md flagged: log only", setup: func(e *env, j *Job) {
			good(e)
			e.write(".ard/7/plan.md", "leaked "+planted())
		}, outcome: OutcomeNeedsHuman, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log"}, reason: ".ard/7/plan.md",
			labels: []string{"ard:needs-human"}},
		{name: "failed and log flagged", setup: func(e *env, j *Job) {
			j.ExitCode = 1
			if err := os.WriteFile(j.LogPath, []byte(planted()), 0o600); err != nil {
				t.Fatal(err)
			}
		}, outcome: OutcomeFailed, files: nil, withhld: true, reason: "agent exited 1",
			labels: []string{"ard:executor:failed", "ard:needs-human"}},
		{name: "symlink", setup: func(e *env, j *Job) {
			good(e)
			if err := os.Symlink("/etc/hosts", filepath.Join(e.repo, ".ard/7/plan.md")); err != nil {
				t.Fatal(err)
			}
		}, outcome: OutcomeFailed, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log"}, reason: "not a regular file",
			labels: []string{"ard:executor:failed"}},
		{name: "101 files", setup: func(e *env, j *Job) {
			good(e)
			for i := 0; i < 100; i++ {
				e.write(fmt.Sprintf(".ard/7/f%03d.md", i), "x")
			}
		}, outcome: OutcomeFailed, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log"}, reason: "101 changed files",
			labels: []string{"ard:executor:failed"}},
		{name: "file over 1 MiB", setup: func(e *env, j *Job) {
			good(e)
			e.write(".ard/7/plan.md", strings.Repeat("x", 1<<20+1))
		}, outcome: OutcomeFailed, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log"}, reason: "larger than 1 MiB",
			labels: []string{"ard:executor:failed"}},
		{name: "no host log", setup: func(e *env, j *Job) { good(e); j.LogPath = "" },
			outcome: OutcomeDone, files: []string{".ard/7/triage-lead.md"}, labels: []string{"ard:triage:lead:done"}},
		{name: "missing host log is withheld", setup: func(e *env, j *Job) {
			good(e)
			j.LogPath = filepath.Join(e.logDir, "gone-20260102T030405Z.log")
		}, outcome: OutcomeNeedsHuman, files: []string{".ard/7/triage-lead.md"}, withhld: true, labels: []string{"ard:needs-human"}},
		// The executor scrubs the role file before the agent runs; setup
		// commits it at base and removes it, as Job.Scrub does.
		{name: "stale role file scrubbed, nothing written", setup: func(e *env, j *Job) {
			e.write(".ard/7/triage-lead.md", roleFile("lead", "done"))
			j.BaseSHA = e.commitAll("stale")
			e.scrub(j, ".ard/7/triage-lead.md")
			e.write(".ard/7/plan.md", "partial")
		}, outcome: OutcomeFailed, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log"},
			reason: "role file not written by this job", labels: []string{"ard:executor:failed"}},
		{name: "stale role file, not scrubbed", setup: func(e *env, j *Job) {
			e.write(".ard/7/triage-lead.md", roleFile("lead", "done"))
			j.BaseSHA = e.commitAll("stale")
		}, outcome: OutcomeFailed, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log"},
			reason: "role file not scrubbed by executor", labels: []string{"ard:executor:failed"}},
		{name: "scrubbed stale file never parsed", setup: func(e *env, j *Job) {
			e.write(".ard/7/triage-lead.md", "no frontmatter")
			j.BaseSHA = e.commitAll("stale")
			e.scrub(j, ".ard/7/triage-lead.md")
		}, outcome: OutcomeFailed, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log"},
			reason: "role file not written by this job", labels: []string{"ard:executor:failed"}},
		{name: "role file rewritten with changes", setup: func(e *env, j *Job) {
			e.write(".ard/7/triage-lead.md", roleFile("lead", "blocked"))
			j.BaseSHA = e.commitAll("stale")
			e.scrub(j, ".ard/7/triage-lead.md")
			good(e)
		}, outcome: OutcomeDone, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log", ".ard/7/triage-lead.md"},
			labels: []string{"ard:triage:lead:done"}},
		{name: "role file rewritten byte for byte", setup: func(e *env, j *Job) {
			good(e)
			j.BaseSHA = e.commitAll("earlier run")
			e.scrub(j, ".ard/7/triage-lead.md")
			good(e)
		}, outcome: OutcomeDone, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log"},
			labels: []string{"ard:triage:lead:done"}},
		{name: "old name only", setup: func(e *env, j *Job) { e.write(".ard/7/lead.md", roleFile("lead", "done")) },
			outcome: OutcomeFailed, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log"}, reason: "role file missing",
			labels: []string{"ard:executor:failed"}},
		{name: "collect failure wins over missing role file", setup: func(e *env, j *Job) {
			for i := 0; i < 101; i++ {
				e.write(fmt.Sprintf(".ard/7/f%03d.md", i), "x")
			}
		}, outcome: OutcomeFailed, files: []string{".ard/7/logs/triage-lead-20260102T030405Z.log"}, reason: "101 changed files",
			labels: []string{"ard:executor:failed"}},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			j := e.claim(StageTriage, "lead")
			c.setup(e, &j)
			rec, err := Prepare(context.Background(), j, e.d)
			if err != nil {
				t.Fatal(err)
			}
			p := rec.Plan
			if rec.Phase != PhasePrepared || rec.Version != recordVersion {
				t.Errorf("record = %+v", rec)
			}
			if p.Outcome != c.outcome {
				t.Errorf("outcome = %s (%s), want %s", p.Outcome, p.Reason, c.outcome)
			}
			if got := paths(p); strings.Join(got, "|") != strings.Join(c.files, "|") {
				t.Errorf("files = %v, want %v", got, c.files)
			}
			if c.reason != "" && !strings.Contains(p.Reason, c.reason) {
				t.Errorf("reason = %q, want %q", p.Reason, c.reason)
			}
			if p.LogWithheld != c.withhld {
				t.Errorf("LogWithheld = %v", p.LogWithheld)
			}
			var names []string
			for _, l := range p.Terminal {
				names = append(names, l.Name)
			}
			if strings.Join(names, "|") != strings.Join(c.labels, "|") {
				t.Errorf("terminal = %v, want %v", names, c.labels)
			}
			// No matched text in the reason, ever.
			if strings.Contains(p.Reason, planted()) {
				t.Error("reason leaks the secret")
			}
		})
	}
}

func TestPrepareRedactsAndStages(t *testing.T) {
	e := newEnv(t)
	j := e.claim(StageTriage, "lead")
	e.write(".ard/7/triage-lead.md", "---\nrole: lead\nstatus: done\nattempt: 1\n---\nUsed "+secretValue+" to check.\n")
	rec, err := Prepare(context.Background(), j, e.d)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".ard/7/triage-lead.md", logPath} {
		got := staged(t, rec.Plan, p)
		if strings.Contains(got, secretValue) || !strings.Contains(got, "[REDACTED:MODEL_KEY]") {
			t.Errorf("%s not redacted: %q", p, got)
		}
	}
	if strings.Contains(rec.Plan.Summary, secretValue) {
		t.Errorf("summary not redacted: %q", rec.Plan.Summary)
	}
	for _, f := range rec.Plan.Files {
		fi, err := os.Stat(f.Staged)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f.Path, fi, err)
		}
	}
	if rec.Plan.LogPath != logPath || rec.Plan.RolePath != ".ard/7/triage-lead.md" {
		t.Errorf("paths: %q %q", rec.Plan.LogPath, rec.Plan.RolePath)
	}
}

type brokenRedactor struct{}

func (brokenRedactor) Log(context.Context, string, string) (redact.LogResult, error) {
	return redact.LogResult{}, errors.New("scanner down")
}
func (brokenRedactor) Text(context.Context, []byte) (redact.TextResult, error) {
	return redact.TextResult{}, errors.New("scanner down")
}

func TestPrepareScannerDownCommitsNothing(t *testing.T) {
	e := newEnv(t)
	e.d.NewRedactor = func(Job) (Redactor, error) { return brokenRedactor{}, nil }
	j := e.claim(StageTriage, "lead")
	e.write(".ard/7/triage-lead.md", roleFile("lead", "done"))
	e.write(".ard/7/plan.md", "x")
	rec, err := Prepare(context.Background(), j, e.d)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Plan.Files) != 0 || rec.Plan.Outcome != OutcomeNeedsHuman || !rec.Plan.LogWithheld {
		t.Errorf("plan = %+v", rec.Plan)
	}
}

func TestPrepareJobValidation(t *testing.T) {
	e := newEnv(t)
	for _, j := range []Job{
		{ID: "a", Stage: "scanner", Role: "x", Issue: 1},
		{ID: "a", Stage: StageReview, Role: "x", Issue: 1, Branch: "worker/x"},
		{ID: "a", Stage: StageWorker, Role: "x", Issue: 1},
		{Stage: StageTriage, Role: "x", Issue: 1},
		{ID: "a", Stage: StageTriage, Role: "x", Issue: 1}, // no BaseSHA
	} {
		if _, err := Prepare(context.Background(), j, e.d); err == nil {
			t.Errorf("job %+v accepted", j)
		}
	}
}

func TestPrepareRoleFileNamePerStage(t *testing.T) {
	tests := []struct {
		stage, role, file string
		extra             []string
	}{
		{StageTriage, "lead", ".ard/7/triage-lead.md", nil},
		{StageWorker, "engineer", ".ard/7/worker-engineer.md", nil},
		{StageReview, "eng-review", ".ard/7/review-eng-review.md", []string{"verdict: approve"}},
	}
	for _, c := range tests {
		t.Run(c.stage, func(t *testing.T) {
			e := newEnv(t)
			j := e.claim(c.stage, c.role)
			e.write(c.file, roleFile(c.role, "done", c.extra...))
			rec, err := Prepare(context.Background(), j, e.d)
			if err != nil {
				t.Fatal(err)
			}
			p := rec.Plan
			if p.Outcome != OutcomeDone || p.RolePath != c.file {
				t.Errorf("outcome %s (%s), RolePath %q, want done and %q", p.Outcome, p.Reason, p.RolePath, c.file)
			}
			if !slices.Contains(paths(p), c.file) {
				t.Errorf("files = %v, want %s", paths(p), c.file)
			}
		})
	}
}

// TestPrepareWorkerIgnoresTriageFileOfSameRole is #127's failure case: triage
// and worker roles named engineer no longer share a file.
func TestPrepareWorkerIgnoresTriageFileOfSameRole(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(e *env, j *Job)
		outcome Outcome
		reason  string
	}{
		{"worker writes nothing", func(e *env, j *Job) {}, OutcomeFailed, "role file missing"},
		{"worker writes its own file", func(e *env, j *Job) {
			e.write(".ard/7/worker-engineer.md", roleFile("engineer", "done"))
		}, OutcomeDone, ""},
		{"earlier round's worker file scrubbed", func(e *env, j *Job) {
			e.write(".ard/7/worker-engineer.md", roleFile("engineer", "done"))
			j.BaseSHA = e.commitAll("earlier round")
			e.scrub(j, ".ard/7/worker-engineer.md")
		}, OutcomeFailed, "role file not written by this job"},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.write(".ard/7/triage-engineer.md", roleFile("engineer", "done"))
			base := e.commitAll("triage")
			j := e.claim(StageWorker, "engineer")
			j.BaseSHA = base
			c.setup(e, &j)
			rec, err := Prepare(context.Background(), j, e.d)
			if err != nil {
				t.Fatal(err)
			}
			p := rec.Plan
			if p.Outcome != c.outcome || !strings.Contains(p.Reason, c.reason) {
				t.Errorf("outcome %s (%s), want %s (%q)", p.Outcome, p.Reason, c.outcome, c.reason)
			}
			if slices.Contains(paths(p), ".ard/7/triage-engineer.md") {
				t.Errorf("triage file committed: %v", paths(p))
			}
		})
	}
}
