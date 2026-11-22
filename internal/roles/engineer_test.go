package roles

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/labels"
)

const engineerPromptPath = "triage/engineer/prompt.txt"

func TestEmbeddedEngineerLoadsAsTriage(t *testing.T) {
	res, err := config.LoadFS(starterFS(t, true), "/repo")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	r, ok := res.Role(config.KindTriage, "engineer")
	if !ok {
		t.Fatalf("no triage role engineer in %+v", res.Roles)
	}
	if r.PromptPath != ".ard/triage/engineer/prompt.txt" || r.Dockerfile != "" || r.Extra != nil || r.ScanPath != "" || len(r.After) != 0 {
		t.Errorf("unexpected role: %+v", r)
	}
	if r.Model != "claude-sonnet-5-5" || r.Timeout != 30*time.Minute || r.Agent != "claude" {
		t.Errorf("model=%q timeout=%v agent=%q", r.Model, r.Timeout, r.Agent)
	}
	// Accepted under #84: no Dockerfile, so the engineer runs as a host
	// subprocess. Adding a Dockerfile is a deliberate test change.
	found := false
	for _, h := range res.HostUntrusted() {
		found = found || (h.Kind == config.KindTriage && h.Name == "engineer")
	}
	if !found {
		t.Errorf("HostUntrusted = %v, want it to include triage/engineer", res.HostUntrusted())
	}
}

func TestEngineerPromptShape(t *testing.T) {
	b, err := FS.ReadFile(engineerPromptPath)
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
		strings.Replace(agent.RoleFilePath(7, "triage", "engineer"), "/7/", "/N/", 1),
		".ard-input/N/issue.md", ".ard-input/N/comments.md", ".ard/N/plan.md",
		"role: engineer", "attempt: 1", "UNTRUSTED",
		"## Implementation", "### Steps", "### Test plan", "### Risk", "### Size",
		"git push", "gh or any other command", "verdict", "worker",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	for _, bad := range []string{".ard/N/engineer.md", "role: triage-engineer", "{{", "${"} {
		if strings.Contains(p, bad) {
			t.Errorf("prompt contains %q", bad)
		}
	}
}

func TestEngineerPromptExamples(t *testing.T) {
	b, err := FS.ReadFile(engineerPromptPath)
	if err != nil {
		t.Fatal(err)
	}
	blocks := fencedFrontmatter(string(b))
	if len(blocks) < 3 {
		t.Fatalf("found %d example blocks, want at least 3", len(blocks))
	}
	stubs := stubRoles{
		config.KindTriage:   {"lead", "engineer"},
		config.KindReviewer: {"license-review"},
	}
	job := closeout.Job{ID: "j", Issue: 7, Stage: closeout.StageTriage, Role: "engineer", Attempt: 1}
	var done, notDone int
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
		p := closeout.Build(job, rf, stubs)
		if rf.Status == closeout.StatusDone {
			done++
			if p.Outcome != closeout.OutcomeDone || len(p.Skipped) != 0 || p.Worker != "" {
				t.Errorf("example %d: outcome=%v skipped=%v worker=%q", i, p.Outcome, p.Skipped, p.Worker)
			}
			for _, l := range p.Remove {
				if l.Name == labels.Pending {
					t.Errorf("example %d: engineer removes %s", i, labels.Pending)
				}
			}
			if len(rf.Next) > 0 && len(p.Deferred) != len(rf.Next) {
				t.Errorf("example %d: deferred=%v want %v", i, p.Deferred, rf.Next)
			}
			continue
		}
		notDone++
		if p.Outcome != closeout.OutcomeNeedsHuman || len(rf.Next) != 0 {
			t.Errorf("example %d: outcome=%v next=%v", i, p.Outcome, rf.Next)
		}
	}
	if done == 0 || notDone == 0 {
		t.Errorf("want a done and a non-done example, got %d and %d", done, notDone)
	}
}

// The prompt warns against each of these; the parser or the close-out role
// check must reject them.
func TestEngineerContractTraps(t *testing.T) {
	const body = "Plan.\n"
	hdr := func(extra string) string {
		return "---\nrole: engineer\nstatus: done\nattempt: 1\n" + extra + "---\n" + body
	}
	tests := []struct {
		name, file string
		wantOK     bool
	}{
		{"valid", hdr(""), true},
		{"role triage-engineer", strings.Replace(hdr(""), "role: engineer", "role: triage-engineer", 1), false},
		{"role triage/engineer", strings.Replace(hdr(""), "role: engineer", "role: triage/engineer", 1), false},
		{"worker", hdr("worker: engineer\n"), false},
		{"verdict", hdr("verdict: approve\n"), false},
		{"extra key", hdr("size: M\n"), false},
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
}
