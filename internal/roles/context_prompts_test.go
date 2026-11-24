package roles

import (
	"strings"
	"testing"
)

var contextDocs = []string{"PRODUCT.md", "VISION.md", "TECH_STACK.md"}

// TestStarterPromptsContextDocs checks each starter prompt names exactly the
// repo context docs its role reads (#291's readers table) and states the
// absent rule, precedence, conflict handling and, for reviewers, the PR-head
// rule.
func TestStarterPromptsContextDocs(t *testing.T) {
	rows := []struct {
		path string
		must []string
		rev  bool
	}{
		{"triage/lead", []string{"PRODUCT.md", "VISION.md"}, false},
		{"triage/product-manager", []string{"PRODUCT.md", "VISION.md"}, false},
		{"triage/designer", []string{"PRODUCT.md"}, false},
		{"triage/documentation-planner", []string{"PRODUCT.md"}, false},
		{"triage/architect", []string{"VISION.md", "TECH_STACK.md"}, false},
		{"triage/engineer", []string{"TECH_STACK.md"}, false},
		{"workers/engineer", []string{"TECH_STACK.md"}, false},
		{"reviewers/product-manager-review", []string{"PRODUCT.md", "VISION.md"}, true},
		{"reviewers/eng-review", []string{"TECH_STACK.md"}, true},
		{"reviewers/license-review", []string{"TECH_STACK.md"}, true},
		{"reviewers/security-review", []string{"TECH_STACK.md"}, true},
	}
	for _, r := range rows {
		t.Run(r.path, func(t *testing.T) {
			b, err := FS.ReadFile(r.path + "/prompt.txt")
			if err != nil {
				t.Fatal(err)
			}
			s := string(b)
			want := map[string]bool{}
			for _, n := range r.must {
				want[n] = true
				if !strings.Contains(s, n) {
					t.Errorf("prompt does not name %s", n)
				}
			}
			for _, n := range contextDocs {
				if !want[n] && strings.Contains(s, n) {
					t.Errorf("prompt names %s, which this role does not read", n)
				}
			}
			flat := strings.Join(strings.Fields(s), " ")
			phrases := []string{
				"optional", "outside headings and HTML comments", "counts as absent",
				"CLAUDE.md and AGENTS.md win on commands and layout",
				"the issue is the ask", "Conflict:",
			}
			if r.rev {
				phrases = append(phrases, "diff.patch does not touch .ard/*.md", "blocking finding of severity high")
			}
			for _, p := range phrases {
				if !strings.Contains(flat, p) {
					t.Errorf("prompt lacks %q", p)
				}
			}
			if !r.rev && strings.Contains(flat, "PR-head rule") {
				t.Error("non-reviewer prompt carries the PR-head rule")
			}
		})
	}
}

func TestTemplateOnlyIsAbsent(t *testing.T) {
	for _, n := range contextDocs {
		b, err := FS.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		if l, ok := onlyHeadings(string(b)); !ok {
			t.Errorf("template %s counts as present: %q", n, l)
		}
	}
	if _, ok := onlyHeadings("# A\n<!-- x\ny -->\n## B\n"); !ok {
		t.Error("headings and comments should count as absent")
	}
	if _, ok := onlyHeadings("# A\n<!-- x -->\nWe build tools.\n"); ok {
		t.Error("a prose line should count as present")
	}
}
