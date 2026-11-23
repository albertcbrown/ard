package roles

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/redact"
)

const workerEngineerPromptPath = "workers/engineer/prompt.txt"

func workerJob() closeout.Job {
	return closeout.Job{ID: "j", Issue: 7, Stage: closeout.StageWorker, Role: "engineer", Attempt: 1, PR: 12,
		Worker: &closeout.WorkerResult{PR: 12, Tests: closeout.TestsPassed}}
}

func TestEmbeddedWorkerEngineerLoadsAsWorker(t *testing.T) {
	res, err := config.LoadFS(starterFS(t, true), "/repo")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	r, ok := res.Role(config.KindWorker, "engineer")
	if !ok {
		t.Fatalf("no worker role engineer in %+v", res.Roles)
	}
	if r.PromptPath != ".ard/workers/engineer/prompt.txt" || r.Dockerfile != "" || r.Extra != nil || r.ScanPath != "" {
		t.Errorf("unexpected role: %+v", r)
	}
	if r.Model != "claude-sonnet-5-5" || r.Timeout != 60*time.Minute || r.Agent != "claude" {
		t.Errorf("model=%q timeout=%v agent=%q", r.Model, r.Timeout, r.Agent)
	}
	if r.Tests != "make test" || r.MaxTestFixes != 2 || r.MaxReviewRounds != 3 || r.Reviewers != nil {
		t.Errorf("tests=%q fixes=%d rounds=%d reviewers=%v", r.Tests, r.MaxTestFixes, r.MaxReviewRounds, r.Reviewers)
	}
	// Accepted under #84 until #55's base image: no Dockerfile, so the worker
	// runs as a host subprocess. Adding a Dockerfile is a deliberate test change.
	found := false
	for _, h := range res.HostUntrusted() {
		found = found || (h.Kind == config.KindWorker && h.Name == "engineer")
	}
	if !found {
		t.Errorf("HostUntrusted = %v, want it to include workers/engineer", res.HostUntrusted())
	}
}

func TestWorkerEngineerPromptShape(t *testing.T) {
	b, err := FS.ReadFile(workerEngineerPromptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(b) || len(b) > 16<<10 {
		t.Fatalf("prompt: valid=%v size=%d", utf8.Valid(b), len(b))
	}
	for i, c := range b {
		if c > 0x7f {
			t.Fatalf("non-ASCII byte at %d", i)
		}
	}
	p := string(b)
	for _, want := range []string{
		strings.Replace(agent.RoleFilePath(7, "worker", "engineer"), "/7/", "/N/", 1),
		".ard-input/N/issue.md", ".ard-input/N/comments.md", ".ard-input/N/test-output.log", ".ard/N/plan.md", ".ard/N/issue.md",
		"## " + config.BuiltinPlanSections["engineer"],
		"role: engineer", "attempt: 1", "UNTRUSTED",
		"git push", "gh or any other command", "verdict", "worker key",
		".github/", ".gitmodules", ".gitattributes", "diff_max_lines", "2,000", "diff_warn_lines", "256", "1 MiB", "symlink", "binary", "at run time",
		"## Changes", "## Testing", "## Open issues", "environment variable",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	for _, bad := range []string{"role: worker-engineer", ".ard/N/engineer.md", "{{", "${", "go test", "make test", "docs/proposal.md", "internal/"} {
		if strings.Contains(p, bad) {
			t.Errorf("prompt contains %q", bad)
		}
	}
	fs, err := redact.Builtin().Scan(context.Background(), bytes.NewReader(b))
	if err != nil || len(fs) != 0 {
		t.Errorf("secret scan of the prompt: %v, %v", fs, err)
	}
}

func TestWorkerEngineerPromptExamples(t *testing.T) {
	b, err := FS.ReadFile(workerEngineerPromptPath)
	if err != nil {
		t.Fatal(err)
	}
	blocks := fencedFrontmatter(string(b))
	if len(blocks) < 4 {
		t.Fatalf("found %d example blocks, want at least 4", len(blocks))
	}
	stubs := stubRoles{config.KindWorker: {"engineer"}, config.KindReviewer: {"license-review"}}
	job := workerJob()
	var doneNext, donePlain, notDone int
	for i, blk := range blocks {
		rf, err := closeout.Parse([]byte(blk))
		if err != nil {
			t.Errorf("example %d: %v", i, err)
			continue
		}
		if rf.Role != "engineer" || rf.Attempt != 1 || rf.Verdict != "" || rf.Worker != "" {
			t.Errorf("example %d: role=%q attempt=%d verdict=%q worker=%q", i, rf.Role, rf.Attempt, rf.Verdict, rf.Worker)
		}
		for _, n := range rf.Next {
			if !strings.HasPrefix(n, "review:") {
				t.Errorf("example %d: next entry %q is not a review", i, n)
			}
		}
		if !strings.Contains(rf.Summary, "## Open issues") {
			t.Errorf("example %d: body lacks ## Open issues", i)
		}
		if len(rf.Summary) >= 3000 {
			t.Errorf("example %d: body is %d bytes", i, len(rf.Summary))
		}
		p := closeout.Build(job, rf, stubs)
		if rf.Status != closeout.StatusDone {
			notDone++
			if p.Outcome != closeout.OutcomeNeedsHuman || len(rf.Next) != 0 {
				t.Errorf("example %d: outcome=%v next=%v", i, p.Outcome, rf.Next)
			}
			continue
		}
		for _, h := range []string{"## Changes", "## Testing"} {
			if !strings.Contains(rf.Summary, h) {
				t.Errorf("example %d: body lacks %s", i, h)
			}
		}
		if p.Outcome != closeout.OutcomeDone || len(p.Skipped) != 0 {
			t.Errorf("example %d: outcome=%v skipped=%v", i, p.Outcome, p.Skipped)
		}
		if !hasLabel(p.Terminal, closeout.Label{Number: 7, Name: labels.Done(labels.StageWorker, "engineer")}) {
			t.Errorf("example %d: terminal = %v", i, p.Terminal)
		}
		for _, n := range rf.Next {
			want := closeout.Label{Number: 12, Name: labels.Request(labels.StageReview, strings.TrimPrefix(n, "review:"))}
			if !hasLabel(p.Requests, want) {
				t.Errorf("example %d: requests %v lack %v", i, p.Requests, want)
			}
		}
		if len(rf.Next) > 0 {
			doneNext++
		} else {
			donePlain++
		}
	}
	if doneNext == 0 || donePlain == 0 || notDone == 0 {
		t.Errorf("want done+next, done and not-done examples, got %d, %d, %d", doneNext, donePlain, notDone)
	}
}

func hasLabel(ls []closeout.Label, want closeout.Label) bool {
	for _, l := range ls {
		if l == want {
			return true
		}
	}
	return false
}

// The prompt warns against each of these; the parser or the close-out role
// check must reject them.
func TestWorkerEngineerContractTraps(t *testing.T) {
	hdr := func(extra string) string {
		return "---\nrole: engineer\nstatus: done\nattempt: 1\n" + extra + "---\nChange.\n"
	}
	tests := []struct {
		name, file string
		wantOK     bool
	}{
		{"valid", hdr(""), true},
		{"role worker-engineer", strings.Replace(hdr(""), "role: engineer", "role: worker-engineer", 1), false},
		{"role workers/engineer", strings.Replace(hdr(""), "role: engineer", "role: workers/engineer", 1), false},
		{"worker", hdr("worker: engineer\n"), false},
		{"verdict", hdr("verdict: approve\n"), false},
		{"extra key", hdr("tests: passed\n"), false},
		{"scalar next", hdr("next: review:x\n"), false},
		{"quoted attempt", strings.Replace(hdr(""), "attempt: 1", "attempt: \"1\"", 1), false},
		{"empty body", "---\nrole: engineer\nstatus: done\nattempt: 1\n---\n", false},
		{"leading blank line", "\n" + hdr(""), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rf, err := closeout.Parse([]byte(tc.file))
			if tc.wantOK {
				if err != nil || rf.Role != "engineer" || rf.Verdict != "" || rf.Worker != "" {
					t.Fatalf("control did not parse cleanly: %+v, %v", rf, err)
				}
				return
			}
			if err != nil {
				return
			}
			if rf.Role == "engineer" && rf.Verdict == "" && rf.Worker == "" {
				t.Errorf("parsed and passes the role checks: %+v", rf)
			}
		})
	}
	rf, err := closeout.Parse([]byte(hdr("next: [triage:architect]\n")))
	if err != nil {
		t.Fatal(err)
	}
	p := closeout.Build(workerJob(), rf, stubRoles{config.KindWorker: {"engineer"}, config.KindTriage: {"architect"}})
	if len(p.Skipped) != 1 || p.Skipped[0].Why != "wrong stage" || len(p.Requests) != 0 {
		t.Errorf("triage next from a worker: skipped=%v requests=%v", p.Skipped, p.Requests)
	}
}
