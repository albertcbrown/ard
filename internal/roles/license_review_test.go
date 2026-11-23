package roles

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
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

const (
	licenseReviewPromptPath    = "reviewers/license-review/prompt.txt"
	licenseReviewAllowlistPath = "reviewers/license-review/allowlist.txt"
	goLicensesCheck            = "go-licenses check ./... --allowed_licenses="
)

var licenseReviewPaths = []string{"**/go.mod", "**/go.sum", "**/package.json", "**/package-lock.json", "**/npm-shrinkwrap.json"}

func licenseJob() closeout.Job {
	j := reviewJob()
	j.Role = "license-review"
	return j
}

func TestEmbeddedLicenseReviewLoadsAsReviewer(t *testing.T) {
	res, err := config.LoadFS(starterFS(t, true), "/repo")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	r, ok := res.Role(config.KindReviewer, "license-review")
	if !ok {
		t.Fatalf("no reviewer role license-review in %+v", res.Roles)
	}
	if r.PromptPath != ".ard/reviewers/license-review/prompt.txt" || r.Dockerfile != "" || r.ScanPath != "" || r.Sidecar != "" ||
		!slices.Equal(r.Extra, []string{"allowlist.txt"}) {
		t.Errorf("unexpected role: %+v", r)
	}
	if r.Model != "claude-sonnet-5-5" || r.Timeout != 30*time.Minute || r.Agent != "claude" {
		t.Errorf("model=%q timeout=%v agent=%q", r.Model, r.Timeout, r.Agent)
	}
	if r.When == nil || !slices.Equal(r.When.Paths, licenseReviewPaths) {
		t.Errorf("When = %+v, want paths %v", r.When, licenseReviewPaths)
	}
	found := false
	for _, h := range res.HostUntrusted() {
		found = found || (h.Kind == config.KindReviewer && h.Name == "license-review")
	}
	if !found {
		t.Errorf("HostUntrusted = %v, want it to include reviewers/license-review", res.HostUntrusted())
	}
	w, ok := res.Role(config.KindWorker, "engineer")
	if !ok || len(w.Reviewers) != 0 {
		t.Errorf("workers/engineer reviewers = %v (ok=%v), want unset", w.Reviewers, ok)
	}
}

func TestLicenseReviewPromptShape(t *testing.T) {
	b, err := FS.ReadFile(licenseReviewPromptPath)
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
		strings.Replace(agent.RoleFilePath(7, "review", "license-review"), "/7/", "/N/", 1),
		".ard-input/N/diff.patch", ".ard-input/N/issue.md", ".ard-input/N/comments.md", ".ard/N/plan.md",
		"role: license-review", "attempt: 1", "status: done or needs-human", "verdict: approve or changes", "UNTRUSTED",
		closeout.SeverityCritical, closeout.SeverityHigh, closeout.SeverityMedium, closeout.SeverityLow, closeout.SeverityInfo,
		"critical, high, medium, low or info",
		fmt.Sprintf("At most %d findings", closeout.MaxFindings), "300 characters", "2,000 characters",
		"message: |", "in double quotes", "blocking: true or false", "Never approve with a blocking finding",
		"$ARD_ROLE_DIR/allowlist.txt", "$ARD_SIDECAR/licenses/allowlist.txt",
		goLicensesCheck, "--ignore", "go-licenses report ./...", "go mod download -json", "package-lock.json",
		"## Checks", "## Dependencies", "No dependency changes to review",
		"git push", "gh or any other command", "next key", "worker key", "environment variable",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	for _, bad := range []string{"{{", "${", "docs/proposal.md", "internal/", "role: review-license-review"} {
		if strings.Contains(p, bad) {
			t.Errorf("prompt contains %q", bad)
		}
	}
	fs, err := redact.Builtin().Scan(context.Background(), bytes.NewReader(b))
	if err != nil || len(fs) != 0 {
		t.Errorf("secret scan of the prompt: %v, %v", fs, err)
	}
}

func TestLicenseReviewPromptExamples(t *testing.T) {
	b, err := FS.ReadFile(licenseReviewPromptPath)
	if err != nil {
		t.Fatal(err)
	}
	blocks := fencedFrontmatter(string(b))
	if len(blocks) < 5 {
		t.Fatalf("found %d example blocks, want at least 5", len(blocks))
	}
	var changes, approveWith, approvePlain, needsHuman, goMod, npmLock int
	for i, blk := range blocks {
		rf, err := closeout.Parse([]byte(blk))
		if err != nil {
			t.Errorf("example %d: %v", i, err)
			continue
		}
		if rf.Role != "license-review" || rf.Attempt != 1 || rf.Worker != "" || len(rf.Next) != 0 {
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
				t.Errorf("example %d finding %d: severity %s, blocking %v", i, j, f.Severity, f.Blocking)
			}
			if f.File == "go.mod" || strings.HasSuffix(f.File, "/go.mod") {
				goMod++
			}
			if f.File == "package-lock.json" || strings.HasSuffix(f.File, "/package-lock.json") {
				npmLock++
			}
		}
		p := closeout.Build(licenseJob(), rf, stubRoles{})
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
		for _, h := range []string{"## Checks", "## Dependencies"} {
			if !strings.Contains(rf.Summary, h) {
				t.Errorf("example %d: body lacks %s", i, h)
			}
		}
		if rf.Verdict == closeout.VerdictChanges {
			changes++
			ok := false
			for _, f := range rf.Findings {
				ok = ok || (f.Blocking && strings.Contains(f.Message, "allowlist"))
			}
			if !ok {
				t.Errorf("example %d: no blocking finding mentions the allowlist", i)
			}
			if p.Outcome != closeout.OutcomeChanges || !hasLabel(p.Terminal, closeout.Label{Number: 12, Name: labels.Changes("license-review")}) {
				t.Errorf("example %d: outcome=%v terminal=%v", i, p.Outcome, p.Terminal)
			}
			continue
		}
		if len(rf.Findings) > 0 {
			approveWith++
		} else {
			approvePlain++
			if !strings.Contains(rf.Summary, "No dependency changes to review") {
				t.Errorf("example %d: plain approve lacks the fixed phrase", i)
			}
		}
		if p.Outcome != closeout.OutcomeDone || !hasLabel(p.Terminal, closeout.Label{Number: 12, Name: labels.Done(labels.StageReview, "license-review")}) {
			t.Errorf("example %d: outcome=%v terminal=%v", i, p.Outcome, p.Terminal)
		}
	}
	if changes == 0 || approveWith == 0 || approvePlain == 0 || needsHuman == 0 || goMod == 0 || npmLock == 0 {
		t.Errorf("example coverage: changes=%d approve+findings=%d approve=%d needs-human=%d go.mod=%d package-lock=%d",
			changes, approveWith, approvePlain, needsHuman, goMod, npmLock)
	}
}

var spdxLine = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+-]*( WITH [A-Za-z0-9.+-]+)?$`)

// parseAllowlist mirrors the format the prompt describes; test-only.
func parseAllowlist(b []byte) ([]string, error) {
	var ids []string
	for i, l := range strings.Split(string(b), "\n") {
		if strings.TrimRight(l, " \t\r") != l {
			return nil, fmt.Errorf("line %d: trailing whitespace", i+1)
		}
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if !spdxLine.MatchString(l) {
			return nil, fmt.Errorf("line %d: %q is not an SPDX id", i+1, l)
		}
		ids = append(ids, l)
	}
	return ids, nil
}

func TestLicenseAllowlist(t *testing.T) {
	b, err := FS.ReadFile(licenseReviewAllowlistPath)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := parseAllowlist(b)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.IsSorted(ids) {
		t.Errorf("not sorted: %v", ids)
	}
	if len(slices.Compact(slices.Clone(ids))) != len(ids) {
		t.Errorf("duplicates in %v", ids)
	}
	for _, want := range []string{"MIT", "Apache-2.0", "BSD-3-Clause"} {
		if !slices.Contains(ids, want) {
			t.Errorf("allowlist lacks %s", want)
		}
	}
	for _, id := range ids {
		for _, bad := range []string{"GPL-", "AGPL-", "LGPL-", "SSPL-", "BUSL-", "CC-BY-NC"} {
			if strings.HasPrefix(id, bad) {
				t.Errorf("copyleft or restricted license %s in the default allowlist", id)
			}
		}
	}
	for name, in := range map[string]string{"bad id": "MIT\nnot a license\n", "trailing space": "MIT \n", "empty": "# only a comment\n"} {
		got, err := parseAllowlist([]byte(in))
		if (name == "empty") != (err == nil && len(got) == 0) {
			t.Errorf("%s: got %v, %v", name, got, err)
		}
	}
	if got, err := parseAllowlist([]byte("GPL-2.0-only WITH Classpath-exception-2.0\n")); err != nil || len(got) != 1 {
		t.Errorf("WITH expression: %v, %v", got, err)
	}
}
