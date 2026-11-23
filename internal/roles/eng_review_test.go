package roles

import (
	"bytes"
	"context"
	"fmt"
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

const engReviewPromptPath = "reviewers/eng-review/prompt.txt"

func reviewJob() closeout.Job {
	return closeout.Job{ID: "j", Issue: 7, Stage: closeout.StageReview, Role: "eng-review", Attempt: 1, PR: 12, BaseSHA: "abc",
		// The live loop always passes a non-nil context; nil means a Reaper
		// close-out after a restart, which is treated as stale.
		Review: &closeout.ReviewContext{}}
}

func TestEmbeddedEngReviewLoadsAsReviewer(t *testing.T) {
	res, err := config.LoadFS(starterFS(t, true), "/repo")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	r, ok := res.Role(config.KindReviewer, "eng-review")
	if !ok {
		t.Fatalf("no reviewer role eng-review in %+v", res.Roles)
	}
	if r.PromptPath != ".ard/reviewers/eng-review/prompt.txt" || r.Dockerfile != "" || r.ScanPath != "" || len(r.Extra) != 0 || r.Sidecar != "" || r.When != nil {
		t.Errorf("unexpected role: %+v", r)
	}
	if r.Model != "claude-sonnet-5-5" || r.Timeout != 30*time.Minute || r.Agent != "claude" {
		t.Errorf("model=%q timeout=%v agent=%q", r.Model, r.Timeout, r.Agent)
	}
	// Accepted under #84 until #55's base image: no Dockerfile, so the
	// reviewer runs as a host subprocess. Adding one is a deliberate change.
	found := false
	for _, h := range res.HostUntrusted() {
		found = found || (h.Kind == config.KindReviewer && h.Name == "eng-review")
	}
	if !found {
		t.Errorf("HostUntrusted = %v, want it to include reviewers/eng-review", res.HostUntrusted())
	}
	// The starter worker requests every reviewer role, so eng-review runs on
	// every green PR.
	hasEng := false
	for _, x := range res.RolesOf(config.KindReviewer) {
		hasEng = hasEng || x.Name == "eng-review"
	}
	if !hasEng {
		t.Errorf("reviewer roles = %+v, want eng-review among them", res.RolesOf(config.KindReviewer))
	}
	w, ok := res.Role(config.KindWorker, "engineer")
	if !ok || len(w.Reviewers) != 0 {
		t.Errorf("workers/engineer reviewers = %v (ok=%v), want unset", w.Reviewers, ok)
	}
}

func TestEngReviewPromptShape(t *testing.T) {
	b, err := FS.ReadFile(engReviewPromptPath)
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
		strings.Replace(agent.RoleFilePath(7, "review", "eng-review"), "/7/", "/N/", 1),
		".ard-input/N/diff.patch", ".ard-input/N/issue.md", ".ard-input/N/comments.md", ".ard/N/plan.md",
		"role: eng-review", "attempt: 1", "status: done or needs-human", "verdict: approve or changes", "UNTRUSTED",
		closeout.SeverityCritical, closeout.SeverityHigh, closeout.SeverityMedium, closeout.SeverityLow, closeout.SeverityInfo,
		"critical, high, medium, low or info",
		fmt.Sprintf("At most %d findings", closeout.MaxFindings), "300 characters", "2,000 characters",
		"message: |", "in double quotes", "blocking: true or false", "Never approve with a blocking finding",
		"inline comments", "1 MiB",
		"## Checks", "Lint:", "lint entry point", "make lint", "own choosing", "no repository lint entry point", "Diff coverage", "mktemp -d",
		"git push", "gh or any other command", "next key", "worker key", "environment variable",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	for _, bad := range []string{"role: review-eng-review", ".ard/N/eng-review.md", "{{", "${", "go test", "make test", "docs/proposal.md", "internal/"} {
		if strings.Contains(p, bad) {
			t.Errorf("prompt contains %q", bad)
		}
	}
	fs, err := redact.Builtin().Scan(context.Background(), bytes.NewReader(b))
	if err != nil || len(fs) != 0 {
		t.Errorf("secret scan of the prompt: %v, %v", fs, err)
	}
}

func TestEngReviewPromptExamples(t *testing.T) {
	b, err := FS.ReadFile(engReviewPromptPath)
	if err != nil {
		t.Fatal(err)
	}
	blocks := fencedFrontmatter(string(b))
	if len(blocks) < 4 {
		t.Fatalf("found %d example blocks, want at least 4", len(blocks))
	}
	var changes, approveWith, approvePlain, needsHuman, withLine, noLine int
	for i, blk := range blocks {
		rf, err := closeout.Parse([]byte(blk))
		if err != nil {
			t.Errorf("example %d: %v", i, err)
			continue
		}
		if rf.Role != "eng-review" || rf.Attempt != 1 || rf.Worker != "" || len(rf.Next) != 0 {
			t.Errorf("example %d: role=%q attempt=%d worker=%q next=%v", i, rf.Role, rf.Attempt, rf.Worker, rf.Next)
		}
		if len(rf.Summary) >= 3000 {
			t.Errorf("example %d: body is %d bytes", i, len(rf.Summary))
		}
		for j, f := range rf.Findings {
			first, _, _ := strings.Cut(f.Message, "\n")
			switch {
			case strings.HasPrefix(f.File, ".ard"):
				t.Errorf("example %d finding %d: file %q", i, j, f.File)
			case len([]rune(f.Message)) > 2000 || len([]rune(first)) > 300:
				t.Errorf("example %d finding %d: message too long", i, j)
			case (f.Severity == closeout.SeverityCritical || f.Severity == closeout.SeverityHigh) && !f.Blocking:
				t.Errorf("example %d finding %d: %s but not blocking", i, j, f.Severity)
			case (f.Severity == closeout.SeverityLow || f.Severity == closeout.SeverityInfo) && f.Blocking:
				t.Errorf("example %d finding %d: %s but blocking", i, j, f.Severity)
			}
			if f.Line > 0 {
				withLine++
			} else {
				noLine++
			}
		}
		p := closeout.Build(reviewJob(), rf, stubRoles{})
		if rf.Status != closeout.StatusDone {
			needsHuman++
			if rf.Verdict != "" || len(rf.Findings) != 0 || p.Outcome != closeout.OutcomeNeedsHuman ||
				!hasLabel(p.Terminal, closeout.Label{Number: 12, Name: labels.NeedsHuman}) {
				t.Errorf("example %d: verdict=%q findings=%d outcome=%v terminal=%v", i, rf.Verdict, len(rf.Findings), p.Outcome, p.Terminal)
			}
			continue
		}
		if rf.Verdict == "" || (rf.Verdict == closeout.VerdictChanges) != closeout.HasBlocking(rf.Findings) {
			t.Errorf("example %d: verdict %q does not follow the blocking flags", i, rf.Verdict)
		}
		if cp := rf; cp.DowngradeVerdict() {
			t.Errorf("example %d relies on the downgrade", i)
		}
		if !strings.Contains(rf.Summary, "## Checks") {
			t.Errorf("example %d: body lacks ## Checks", i)
		}
		if rf.Verdict == closeout.VerdictChanges {
			changes++
			if p.Outcome != closeout.OutcomeChanges || !hasLabel(p.Terminal, closeout.Label{Number: 12, Name: labels.Changes("eng-review")}) {
				t.Errorf("example %d: outcome=%v terminal=%v", i, p.Outcome, p.Terminal)
			}
			continue
		}
		if len(rf.Findings) > 0 {
			approveWith++
		} else {
			approvePlain++
		}
		if p.Outcome != closeout.OutcomeDone || !hasLabel(p.Terminal, closeout.Label{Number: 12, Name: labels.Done(labels.StageReview, "eng-review")}) {
			t.Errorf("example %d: outcome=%v terminal=%v", i, p.Outcome, p.Terminal)
		}
	}
	if changes == 0 || approveWith == 0 || approvePlain == 0 || needsHuman == 0 || withLine == 0 || noLine == 0 {
		t.Errorf("example coverage: changes=%d approve+findings=%d approve=%d needs-human=%d line=%d noline=%d",
			changes, approveWith, approvePlain, needsHuman, withLine, noLine)
	}
}

// The prompt warns against each of these; the parser or the close-out role
// check must reject them.
func TestEngReviewContractTraps(t *testing.T) {
	hdr := func(extra string) string {
		return "---\nrole: eng-review\nstatus: done\nattempt: 1\nverdict: approve\n" + extra + "---\nBody.\n"
	}
	one := func(fields string) string { return hdr("findings:\n  - file: \"a.go\"\n" + fields) }
	many := strings.Repeat("  - file: \"a.go\"\n    severity: low\n    message: m\n", closeout.MaxFindings+1)
	tests := []struct {
		name, file string
		wantOK     bool
		byCheck    bool // parses; closeout.check rejects it (see closeout/review_test.go)
	}{
		{"valid", hdr(""), true, false},
		{"empty findings", hdr("findings: []\n"), true, false},
		{"block message", one("    severity: low\n    message: |\n      Colon: here, `ticks`, \"quotes\" and # hash.\n"), true, false},
		{"needs-human", "---\nrole: eng-review\nstatus: needs-human\nattempt: 1\n---\nNothing to review.\n", true, false},
		{"role review-eng-review", strings.Replace(hdr(""), "role: eng-review", "role: review-eng-review", 1), false, true},
		{"role reviewers/eng-review", strings.Replace(hdr(""), "role: eng-review", "role: reviewers/eng-review", 1), false, true},
		{"verdict lgtm", strings.Replace(hdr(""), "approve", "lgtm", 1), false, false},
		{"done without verdict", strings.Replace(hdr(""), "verdict: approve\n", "", 1), false, true},
		{"worker", hdr("worker: engineer\n"), false, true},
		{"extra key", hdr("score: 3\n"), false, false},
		{"finding key lines", one("    lines: 3\n    severity: low\n    message: m\n"), false, false},
		{"severity warning", one("    severity: warning\n    message: m\n"), false, false},
		{"blocking yes", one("    severity: low\n    blocking: yes\n    message: m\n"), false, false},
		{"quoted line", one("    line: \"4\"\n    severity: low\n    message: m\n"), false, false},
		{"line 0", one("    line: 0\n    severity: low\n    message: m\n"), false, false},
		{"backtick message", one("    severity: low\n    message: `x` is wrong\n"), false, false},
		{"colon message", one("    severity: low\n    message: bad: really\n"), false, false},
		{"numeric file", hdr("findings:\n  - file: 2024\n    severity: low\n    message: m\n"), false, false},
		{"absolute file", hdr("findings:\n  - file: \"/abs.go\"\n    severity: low\n    message: m\n"), false, false},
		{"dotdot file", hdr("findings:\n  - file: \"a/../b.go\"\n    severity: low\n    message: m\n"), false, false},
		{"51 findings", hdr("findings:\n" + many), false, false},
		{"empty message", one("    severity: low\n    message: \"\"\n"), false, false},
		{"empty body", "---\nrole: eng-review\nstatus: done\nattempt: 1\nverdict: approve\n---\n", false, false},
		{"leading blank line", "\n" + hdr(""), false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rf, err := closeout.Parse([]byte(tc.file))
			if tc.wantOK {
				if err != nil || rf.Role != "eng-review" {
					t.Fatalf("control did not parse cleanly: %+v, %v", rf, err)
				}
				if tc.name == "valid" && rf.Verdict != "approve" {
					t.Errorf("verdict = %q", rf.Verdict)
				}
				if tc.name == "block message" && (len(rf.Findings) != 1 || !strings.Contains(rf.Findings[0].Message, "Colon: here, `ticks`, \"quotes\" and # hash.")) {
					t.Errorf("message not kept whole: %+v", rf.Findings)
				}
				return
			}
			if tc.byCheck {
				// Parse accepts these; closeout.check (closeout/review_test.go)
				// rejects them against the job. Assert the bad field is present.
				if err != nil {
					t.Fatalf("want a parse and a later check failure, got %v", err)
				}
				if rf.Role == "eng-review" && rf.Worker == "" && (rf.Status != "done" || rf.Verdict != "") {
					t.Errorf("passes the role checks: %+v", rf)
				}
				return
			}
			if err == nil {
				t.Errorf("parsed without error: %+v", rf)
			}
		})
	}

	rf, err := closeout.Parse([]byte(one("    severity: high\n    blocking: true\n    message: m\n")))
	if err != nil {
		t.Fatal(err)
	}
	if !rf.DowngradeVerdict() || rf.Verdict != closeout.VerdictChanges {
		t.Errorf("approve with a blocking finding: verdict=%q", rf.Verdict)
	}
	rf, err = closeout.Parse([]byte(hdr("next: [review:license-review]\n")))
	if err != nil {
		t.Fatal(err)
	}
	p := closeout.Build(reviewJob(), rf, stubRoles{config.KindReviewer: {"license-review"}})
	if len(p.Skipped) != 1 || p.Skipped[0].Why != "wrong stage" || len(p.Requests) != 0 {
		t.Errorf("review next: skipped=%v requests=%v", p.Skipped, p.Requests)
	}
}
