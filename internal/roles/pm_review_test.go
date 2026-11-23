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
	"github.com/albertcbrown/ard/internal/redact"
)

const pmReviewPromptPath = "reviewers/product-manager-review/prompt.txt"

func pmReviewJob() closeout.Job {
	j := reviewJob()
	j.Role = "product-manager-review"
	return j
}

func TestEmbeddedPMReviewLoadsAsReviewer(t *testing.T) {
	res, err := config.LoadFS(starterFS(t, true), "/repo")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	r, ok := res.Role(config.KindReviewer, "product-manager-review")
	if !ok {
		t.Fatalf("no reviewer role product-manager-review in %+v", res.Roles)
	}
	if r.PromptPath != ".ard/reviewers/product-manager-review/prompt.txt" || r.Dockerfile != "" || r.ScanPath != "" || len(r.Extra) != 0 || r.Sidecar != "" {
		t.Errorf("unexpected role: %+v", r)
	}
	if r.Model != "claude-sonnet-5-5" || r.Timeout != 30*time.Minute || r.Agent != "claude" {
		t.Errorf("model=%q timeout=%v agent=%q", r.Model, r.Timeout, r.Agent)
	}
	if r.When == nil || len(r.When.Triage) != 1 || r.When.Triage[0] != "product-manager" || len(r.When.Paths) != 0 {
		t.Errorf("When = %+v, want triage [product-manager] only", r.When)
	}
	found := false
	for _, h := range res.HostUntrusted() {
		found = found || (h.Kind == config.KindReviewer && h.Name == "product-manager-review")
	}
	if !found {
		t.Errorf("HostUntrusted = %v, want it to include reviewers/product-manager-review", res.HostUntrusted())
	}
}

func TestPMReviewWhenSelection(t *testing.T) {
	res, err := config.LoadFS(starterFS(t, true), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := res.Role(config.KindReviewer, "product-manager-review")
	ran := func(roles ...string) func(string) bool {
		return func(n string) bool {
			for _, x := range roles {
				if x == n {
					return true
				}
			}
			return false
		}
	}
	if !r.When.Matches(nil, ran("product-manager")) {
		t.Error("want a match when product-manager triage ran")
	}
	if r.When.Matches([]string{}, ran()) || r.When.Matches([]string{"x.go"}, ran()) {
		t.Error("want no match without product-manager triage")
	}
	e, _ := res.Role(config.KindReviewer, "eng-review")
	if !e.When.Matches([]string{"x.go"}, ran()) {
		t.Error("eng-review must always match")
	}
}

func TestPMReviewNeedsPMTriageRole(t *testing.T) {
	m := starterFS(t, true)
	for k := range m {
		if strings.HasPrefix(k, ".ard/triage/product-manager/") {
			delete(m, k)
		}
	}
	_, err := config.LoadFS(m, "/repo")
	if err == nil || !strings.Contains(err.Error(), `no such triage role "product-manager"`) {
		t.Errorf("LoadFS err = %v", err)
	}
}

func TestPMReviewPromptShape(t *testing.T) {
	b, err := FS.ReadFile(pmReviewPromptPath)
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
		strings.Replace(agent.RoleFilePath(7, "review", "product-manager-review"), "/7/", "/N/", 1),
		".ard-input/N/diff.patch", ".ard-input/N/issue.md", ".ard-input/N/comments.md", ".ard/N/plan.md",
		"## Requirements", "acceptance criteria", "out of scope", "AC1", "not met", "cannot verify",
		"## Acceptance criteria", "Done when", "$ARD_SIDECAR", "UNTRUSTED",
		closeout.SeverityCritical, closeout.SeverityHigh, closeout.SeverityMedium, closeout.SeverityLow, closeout.SeverityInfo,
		"Never approve with a blocking finding", "1 MiB", "git push",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	for _, bad := range []string{"role: review-product-manager-review", "{{", "${", "docs/proposal.md", "internal/", "make lint", "go test", "make test", "Diff coverage", "## Checks"} {
		if strings.Contains(p, bad) {
			t.Errorf("prompt contains %q", bad)
		}
	}
	fs, err := redact.Builtin().Scan(context.Background(), bytes.NewReader(b))
	if err != nil || len(fs) != 0 {
		t.Errorf("secret scan of the prompt: %v, %v", fs, err)
	}
}

func TestPMReviewPromptExamples(t *testing.T) {
	b, err := FS.ReadFile(pmReviewPromptPath)
	if err != nil {
		t.Fatal(err)
	}
	blocks := fencedFrontmatter(string(b))
	if len(blocks) < 5 {
		t.Fatalf("found %d example blocks, want at least 5", len(blocks))
	}
	var changes, approveWith, approvePlain, needsHuman, withLine, noLine, cannotVerify int
	for i, blk := range blocks {
		rf, err := closeout.Parse([]byte(blk))
		if err != nil {
			t.Errorf("example %d: %v", i, err)
			continue
		}
		if rf.Role != "product-manager-review" || rf.Attempt != 1 || rf.Worker != "" || len(rf.Next) != 0 {
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
			case (f.Severity == closeout.SeverityCritical || f.Severity == closeout.SeverityHigh) != f.Blocking:
				t.Errorf("example %d finding %d: severity %s with blocking=%v", i, j, f.Severity, f.Blocking)
			case strings.HasPrefix(f.Message, "AC") && !f.Blocking && f.Severity != closeout.SeverityMedium:
				t.Errorf("example %d finding %d: non-blocking AC finding must be medium", i, j)
			case strings.Contains(f.Message, "cannot verify") && (f.Severity != closeout.SeverityMedium || f.Blocking):
				t.Errorf("example %d finding %d: cannot verify must be medium and non-blocking", i, j)
			}
			if strings.Contains(f.Message, "cannot verify") {
				cannotVerify++
			}
			if f.Line > 0 {
				withLine++
			} else {
				noLine++
			}
		}
		p := closeout.Build(pmReviewJob(), rf, stubRoles{})
		if rf.Status != closeout.StatusDone {
			needsHuman++
			if rf.Verdict != "" || len(rf.Findings) != 0 || p.Outcome != closeout.OutcomeNeedsHuman {
				t.Errorf("example %d: verdict=%q findings=%d outcome=%v", i, rf.Verdict, len(rf.Findings), p.Outcome)
			}
			continue
		}
		if rf.Verdict == "" || (rf.Verdict == closeout.VerdictChanges) != closeout.HasBlocking(rf.Findings) {
			t.Errorf("example %d: verdict %q does not follow the blocking flags", i, rf.Verdict)
		}
		if cp := rf; cp.DowngradeVerdict() {
			t.Errorf("example %d relies on the downgrade", i)
		}
		if !strings.Contains(rf.Summary, "## Acceptance criteria") {
			t.Errorf("example %d: body lacks ## Acceptance criteria", i)
		}
		switch {
		case rf.Verdict == closeout.VerdictChanges:
			changes++
			if p.Outcome != closeout.OutcomeChanges {
				t.Errorf("example %d: outcome=%v", i, p.Outcome)
			}
		case len(rf.Findings) > 0:
			approveWith++
		default:
			approvePlain++
		}
	}
	if changes == 0 || approveWith == 0 || approvePlain == 0 || needsHuman == 0 || withLine == 0 || noLine == 0 || cannotVerify == 0 {
		t.Errorf("example coverage: changes=%d approve+findings=%d approve=%d needs-human=%d line=%d noline=%d cannot-verify=%d",
			changes, approveWith, approvePlain, needsHuman, withLine, noLine, cannotVerify)
	}
}

func TestPMReviewContractTraps(t *testing.T) {
	hdr := func(role, extra string) string {
		return "---\nrole: " + role + "\nstatus: done\nattempt: 1\nverdict: approve\n" + extra + "---\nBody.\n"
	}
	for _, role := range []string{"review-product-manager-review", "eng-review", "product-manager"} {
		rf, err := closeout.Parse([]byte(hdr(role, "")))
		if err != nil {
			t.Fatal(err)
		}
		if rf.Role == pmReviewJob().Role {
			t.Errorf("role %q equals the job role", role)
		}
	}
	rf, err := closeout.Parse([]byte(hdr("product-manager-review", "findings:\n  - file: \"a.txt\"\n    severity: critical\n    blocking: true\n    message: m\n")))
	if err != nil {
		t.Fatal(err)
	}
	if !rf.DowngradeVerdict() || rf.Verdict != closeout.VerdictChanges {
		t.Errorf("approve with a blocking finding: verdict=%q", rf.Verdict)
	}
	rf, err = closeout.Parse([]byte(hdr("product-manager-review", "next: [review:license-review]\n")))
	if err != nil {
		t.Fatal(err)
	}
	p := closeout.Build(pmReviewJob(), rf, stubRoles{config.KindReviewer: {"license-review"}})
	if len(p.Skipped) != 1 || p.Skipped[0].Why != "wrong stage" || len(p.Requests) != 0 {
		t.Errorf("review next: skipped=%v requests=%v", p.Skipped, p.Requests)
	}
}
