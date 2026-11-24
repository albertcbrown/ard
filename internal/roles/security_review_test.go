package roles

import (
	"bytes"
	"context"
	"fmt"
	"slices"
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

const securityReviewPromptPath = "reviewers/security-review/prompt.txt"

var securityTags = []string{"[injection]", "[authz]", "[secret]", "[dependency]", "[other]"}

func securityJob() closeout.Job {
	j := reviewJob()
	j.Role = "security-review"
	return j
}

func reviewerNames(res *config.Resolved) []string {
	var out []string
	for _, r := range res.RolesOf(config.KindReviewer) {
		out = append(out, r.Name)
	}
	slices.Sort(out)
	return out
}

func TestEmbeddedSecurityReviewLoadsAsReviewer(t *testing.T) {
	res, err := config.LoadFS(starterFS(t, true), "/repo")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	r, ok := res.Role(config.KindReviewer, "security-review")
	if !ok {
		t.Fatalf("no reviewer role security-review in %+v", res.Roles)
	}
	if r.PromptPath != ".ard/reviewers/security-review/prompt.txt" || r.Dockerfile != ".ard/reviewers/security-review/Dockerfile" || r.AidPath != ".ard/reviewers/security-review/aid" || r.ScanPath != "" || strings.Join(r.Extra, ",") != "normalize.jq,rules/security.yml" || r.Sidecar != "" || r.When != nil {
		t.Errorf("unexpected role: %+v", r)
	}
	if r.Model != "claude-opus-5-5" || r.Timeout != 30*time.Minute || r.Agent != "claude" {
		t.Errorf("model=%q timeout=%v agent=%q", r.Model, r.Timeout, r.Agent)
	}
	// The role has a Dockerfile, so it never runs on the host.
	for _, h := range res.HostUntrusted() {
		if h.Kind == config.KindReviewer && h.Name == "security-review" {
			t.Errorf("HostUntrusted includes reviewers/security-review, which has a Dockerfile")
		}
	}
	if got := strings.Join(reviewerNames(res), ","); got != "eng-review,license-review,product-manager-review,security-review" {
		t.Errorf("reviewer roles = %s", got)
	}
}

func TestSecurityReviewPromptShape(t *testing.T) {
	b, err := FS.ReadFile(securityReviewPromptPath)
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
	wants := []string{
		strings.Replace(agent.RoleFilePath(7, "review", "security-review"), "/7/", "/N/", 1),
		".ard-input/N/diff.patch", ".ard-input/N/issue.md", ".ard-input/N/comments.md", ".ard/N/plan.md",
		"role: security-review", "attempt: 1", "status: done or needs-human", "verdict: approve or changes", "UNTRUSTED",
		closeout.SeverityCritical, closeout.SeverityHigh, closeout.SeverityMedium, closeout.SeverityLow, closeout.SeverityInfo,
		"critical, high, medium, low or info",
		fmt.Sprintf("At most %d findings", closeout.MaxFindings), "300 characters", "2,000 characters",
		"message: |", "in double quotes", "blocking: true or false", "Never approve with a blocking finding",
		"inline comments", "1 MiB", "## Checks", "mktemp -d",
		"injection", "authz", "secrets", "unsafe dependencies",
		"aid.jsonl", "Secret scan:", "gitleaks", "--redact", "Static analysis:", "gosec", "-nosec", "semgrep", "--metrics=off", "--disable-nosem", "Dependencies:",
		"Never copy a secret", "Never build, test or run", "suppression",
		"git push", "gh or any other command", "next key", "worker key", "environment variable",
	}
	for _, want := range append(wants, securityTags...) {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	for _, bad := range []string{"role: review-security-review\n", ".ard/N/security-review.md", "{{", "${", "go test", "make test", "docs/proposal.md", "internal/", "--config auto", "--config=auto"} {
		if strings.Contains(p, bad) {
			t.Errorf("prompt contains %q", bad)
		}
	}
	fs, err := redact.Builtin().Scan(context.Background(), bytes.NewReader(b))
	if err != nil || len(fs) != 0 {
		t.Errorf("secret scan of the prompt: %v, %v", fs, err)
	}
}

func TestSecurityReviewPromptExamples(t *testing.T) {
	b, err := FS.ReadFile(securityReviewPromptPath)
	if err != nil {
		t.Fatal(err)
	}
	blocks := fencedFrontmatter(string(b))
	if len(blocks) < 4 {
		t.Fatalf("found %d example blocks, want at least 4", len(blocks))
	}
	var changes, approveWith, approvePlain, needsHuman, withLine, noLine int
	kinds := map[string]bool{}
	for i, blk := range blocks {
		rf, err := closeout.Parse([]byte(blk))
		if err != nil {
			t.Errorf("example %d: %v", i, err)
			continue
		}
		if rf.Role != "security-review" || rf.Attempt != 1 || rf.Worker != "" || len(rf.Next) != 0 {
			t.Errorf("example %d: role=%q attempt=%d worker=%q next=%v", i, rf.Role, rf.Attempt, rf.Worker, rf.Next)
		}
		if len(rf.Summary) >= 3000 {
			t.Errorf("example %d: body is %d bytes", i, len(rf.Summary))
		}
		for j, f := range rf.Findings {
			first, _, _ := strings.Cut(f.Message, "\n")
			tagged := false
			for _, tag := range securityTags {
				if strings.HasPrefix(first, tag+" ") {
					tagged = true
					kinds[tag] = true
				}
			}
			switch {
			case !tagged:
				t.Errorf("example %d finding %d: first line lacks a kind tag", i, j)
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
		p := closeout.Build(securityJob(), rf, stubRoles{})
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
			if p.Outcome != closeout.OutcomeChanges || !hasLabel(p.Terminal, closeout.Label{Number: 12, Name: labels.Changes("security-review")}) {
				t.Errorf("example %d: outcome=%v terminal=%v", i, p.Outcome, p.Terminal)
			}
			continue
		}
		if len(rf.Findings) > 0 {
			approveWith++
		} else {
			approvePlain++
		}
		if p.Outcome != closeout.OutcomeDone || !hasLabel(p.Terminal, closeout.Label{Number: 12, Name: labels.Done(labels.StageReview, "security-review")}) {
			t.Errorf("example %d: outcome=%v terminal=%v", i, p.Outcome, p.Terminal)
		}
	}
	if changes == 0 || approveWith == 0 || approvePlain == 0 || needsHuman == 0 || withLine == 0 || noLine == 0 {
		t.Errorf("example coverage: changes=%d approve+findings=%d approve=%d needs-human=%d line=%d noline=%d",
			changes, approveWith, approvePlain, needsHuman, withLine, noLine)
	}
	for _, tag := range securityTags[:4] {
		if !kinds[tag] {
			t.Errorf("no example finding tagged %s", tag)
		}
	}
}

func TestSecurityReviewRoleTraps(t *testing.T) {
	for _, role := range []string{"review-security-review", "reviewers/security-review", "eng-review", "security"} {
		file := "---\nrole: " + role + "\nstatus: done\nattempt: 1\nverdict: approve\n---\nBody.\n"
		rf, err := closeout.Parse([]byte(file))
		if err != nil {
			t.Errorf("role %q: %v", role, err)
			continue
		}
		if rf.Role == securityJob().Role {
			t.Errorf("role %q equals the job's role", role)
		}
	}
	file := "---\nrole: security-review\nstatus: done\nattempt: 1\nverdict: approve\nfindings:\n  - file: \"a.go\"\n    line: 3\n    severity: critical\n    blocking: true\n    message: |\n      [injection] m\n---\nBody.\n"
	rf, err := closeout.Parse([]byte(file))
	if err != nil {
		t.Fatal(err)
	}
	if !rf.DowngradeVerdict() || rf.Verdict != closeout.VerdictChanges {
		t.Errorf("verdict = %q, want downgrade to changes", rf.Verdict)
	}
}
