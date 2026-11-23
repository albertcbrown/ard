package roles

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
)

var triageRoleCases = []struct {
	name, section, model string
	after                []string
	prompts              []string
}{
	{"product-manager", "Requirements", "claude-opus-5-5", nil, []string{"### Requirements", "### Acceptance criteria", "### Out of scope"}},
	{"architect", "Design", "claude-opus-5-5", []string{"product-manager"}, []string{"### Approach", "### Affected modules", "ADR needed: yes", "ADR needed: no"}},
	{"designer", "UX", "claude-haiku-5-5", nil, []string{"### Flows", "### Messages", ".ard/N/design/", "command line"}},
	{"documentation-planner", "Docs", "claude-haiku-5-5", []string{"designer"}, []string{"### Docs to change", "### Docs not changed"}},
}

func TestTriageRolesLoad(t *testing.T) {
	res, err := config.LoadFS(starterFS(t, true), "/repo")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	for _, tc := range triageRoleCases {
		r, ok := res.Role(config.KindTriage, tc.name)
		if !ok {
			t.Errorf("no triage role %s", tc.name)
			continue
		}
		if r.Model != tc.model || r.Timeout != 30*time.Minute || r.Agent != "claude" || r.Dockerfile != "" {
			t.Errorf("%s: model=%q timeout=%v agent=%q dockerfile=%q", tc.name, r.Model, r.Timeout, r.Agent, r.Dockerfile)
		}
		if !slices.Equal(r.After, tc.after) {
			t.Errorf("%s: After=%v want %v", tc.name, r.After, tc.after)
		}
		if r.PlanSection != tc.section || config.BuiltinPlanSections[tc.name] != tc.section {
			t.Errorf("%s: section=%q want %q", tc.name, r.PlanSection, tc.section)
		}
	}
}

func TestTriageRolesSectionsUnique(t *testing.T) {
	seen := map[string]string{}
	for _, tc := range triageRoleCases {
		if o, dup := seen[tc.section]; dup {
			t.Errorf("%s and %s share section %q", o, tc.name, tc.section)
		}
		seen[tc.section] = tc.name
	}
}

func TestTriageRolesWritable(t *testing.T) {
	for _, tc := range triageRoleCases {
		w := closeout.WritableFor(7, closeout.StageTriage, tc.name, tc.section)
		if w.RoleFile != agent.RoleFilePath(7, "triage", tc.name) || w.Section != tc.section {
			t.Errorf("%s: %+v", tc.name, w)
		}
		wantPrefix := tc.name == "designer"
		if wantPrefix != (len(w.Prefixes) == 1 && w.Prefixes[0] == ".ard/7/design/") || (!wantPrefix && len(w.Prefixes) != 0) {
			t.Errorf("%s: prefixes %v", tc.name, w.Prefixes)
		}
	}
}

func TestTriageRolePrompts(t *testing.T) {
	stubs := stubRoles{
		config.KindTriage:   {"lead", "engineer", "product-manager", "architect", "designer", "documentation-planner"},
		config.KindReviewer: {"license-review"},
	}
	for _, tc := range triageRoleCases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := FS.ReadFile("triage/" + tc.name + "/prompt.txt")
			if err != nil {
				t.Fatal(err)
			}
			if !utf8.Valid(b) || len(b) > 16<<10 {
				t.Fatalf("valid=%v size=%d", utf8.Valid(b), len(b))
			}
			for i, c := range b {
				if c > 0x7f {
					t.Fatalf("non-ASCII byte at %d", i)
				}
			}
			p := string(b)
			for _, want := range append([]string{
				strings.Replace(agent.RoleFilePath(7, "triage", tc.name), "/7/", "/N/", 1),
				".ard-input/N/issue.md", ".ard-input/N/comments.md", ".ard/N/plan.md",
				"role: " + tc.name, "attempt: 1", "UNTRUSTED", "## " + tc.section,
				"git push", "gh or any other command", "verdict",
			}, tc.prompts...) {
				if !strings.Contains(p, want) {
					t.Errorf("prompt lacks %q", want)
				}
			}
			for _, bad := range []string{"role: triage-" + tc.name, "next: [triage", "{{", "${"} {
				if strings.Contains(p, bad) {
					t.Errorf("prompt contains %q", bad)
				}
			}
			blocks := fencedFrontmatter(p)
			if len(blocks) < 3 {
				t.Fatalf("found %d example blocks, want at least 3", len(blocks))
			}
			job := closeout.Job{ID: "j", Issue: 7, Stage: closeout.StageTriage, Role: tc.name, Attempt: 1}
			var done, notDone int
			for i, blk := range blocks {
				rf, err := closeout.Parse([]byte(blk))
				if err != nil {
					t.Errorf("example %d: %v", i, err)
					continue
				}
				if rf.Role != tc.name || rf.Attempt != 1 || rf.Verdict != "" || rf.Worker != "" || len(rf.Next) != 0 {
					t.Errorf("example %d: %+v", i, rf)
				}
				out := closeout.Build(job, rf, stubs)
				if rf.Status == closeout.StatusDone {
					done++
					if out.Outcome != closeout.OutcomeDone {
						t.Errorf("example %d: outcome=%v", i, out.Outcome)
					}
				} else {
					notDone++
					if out.Outcome != closeout.OutcomeNeedsHuman {
						t.Errorf("example %d: outcome=%v", i, out.Outcome)
					}
				}
			}
			if done == 0 || notDone == 0 {
				t.Errorf("want done and non-done examples, got %d and %d", done, notDone)
			}
		})
	}
}
