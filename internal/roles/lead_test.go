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

const leadPromptPath = "triage/lead/prompt.txt"

type stubRoles map[config.Kind][]string

func (s stubRoles) Role(k config.Kind, name string) (config.Role, bool) {
	for _, n := range s[k] {
		if n == name {
			return config.Role{Kind: k, Name: name}, true
		}
	}
	return config.Role{}, false
}

var leadStubRoles = stubRoles{
	config.KindTriage:   {"architect", "product-manager", "designer", "documentation-planner", "engineer"},
	config.KindWorker:   {"engineer"},
	config.KindReviewer: {"eng-review"},
}

func TestEmbeddedLeadLoadsAsTriage(t *testing.T) {
	res, err := config.LoadFS(starterFS(t, true), "/repo")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	lead, ok := res.Role(config.KindTriage, "lead")
	if !ok {
		t.Fatalf("no triage role lead in %+v", res.Roles)
	}
	if lead.PromptPath != ".ard/triage/lead/prompt.txt" || lead.Dockerfile != "" || lead.Extra != nil || lead.ScanPath != "" {
		t.Errorf("unexpected role: %+v", lead)
	}
	if lead.Model != "claude-opus-5-5" || lead.Timeout != 15*time.Minute || lead.Agent != "claude" {
		t.Errorf("model=%q timeout=%v agent=%q", lead.Model, lead.Timeout, lead.Agent)
	}
	// Accepted under #84: no Dockerfile, so the lead runs as a host subprocess
	// and config check warns. Adding a Dockerfile is a deliberate test change.
	found := false
	for _, r := range res.HostUntrusted() {
		found = found || (r.Kind == config.KindTriage && r.Name == "lead")
	}
	if !found {
		t.Errorf("HostUntrusted = %v, want it to include triage/lead", res.HostUntrusted())
	}
}

func TestLeadPromptShape(t *testing.T) {
	b, err := FS.ReadFile(leadPromptPath)
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
	for _, want := range []string{
		"role: lead", "attempt: 1", "never triage:lead", "UNTRUSTED", strings.Replace(agent.RoleFilePath(7, "triage", "lead"), "/7/", "/N/", 1),
		".ard-input/N/issue.md", ".ard-input/N/comments.md", "git push", "gh or any other command", "verdict", ".ard/workers/",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Contains(string(b), ".ard/N/lead.md") {
		t.Error("prompt names the old role file .ard/N/lead.md")
	}
}

// fencedFrontmatter returns the contents of every ``` block whose first line
// is "---".
func fencedFrontmatter(prompt string) []string {
	var out []string
	parts := strings.Split(prompt, "```")
	for i := 1; i < len(parts); i += 2 {
		blk := strings.TrimPrefix(parts[i], "\n")
		if strings.HasPrefix(blk, "---\n") {
			out = append(out, blk)
		}
	}
	return out
}

func TestLeadPromptExamples(t *testing.T) {
	b, err := FS.ReadFile(leadPromptPath)
	if err != nil {
		t.Fatal(err)
	}
	blocks := fencedFrontmatter(string(b))
	if len(blocks) < 3 {
		t.Fatalf("found %d example blocks, want at least 3", len(blocks))
	}
	job := closeout.Job{ID: "j", Issue: 7, Stage: closeout.StageTriage, Role: "lead", Attempt: 1}
	for i, blk := range blocks {
		rf, err := closeout.Parse([]byte(blk))
		if err != nil {
			t.Errorf("example %d: %v", i, err)
			continue
		}
		if rf.Role != "lead" || rf.Attempt != 1 || rf.Verdict != "" {
			t.Errorf("example %d: role=%q attempt=%d verdict=%q", i, rf.Role, rf.Attempt, rf.Verdict)
		}
		p := closeout.Build(job, rf, leadStubRoles)
		if rf.Status == closeout.StatusDone {
			if p.Outcome != closeout.OutcomeDone || len(p.Skipped) != 0 || p.Worker == "" {
				t.Errorf("example %d: outcome=%v skipped=%v worker=%q", i, p.Outcome, p.Skipped, p.Worker)
			}
			pending := false
			for _, l := range p.Remove {
				pending = pending || l.Name == labels.Pending
			}
			if !pending {
				t.Errorf("example %d: plan does not remove %s: %+v", i, labels.Pending, p.Remove)
			}
			continue
		}
		if p.Outcome != closeout.OutcomeNeedsHuman || len(rf.Next) != 0 || rf.Worker != "" {
			t.Errorf("example %d: outcome=%v next=%v worker=%q", i, p.Outcome, rf.Next, rf.Worker)
		}
	}
}

// The prompt warns against each of these; the parser must really reject them.
func TestLeadContractTraps(t *testing.T) {
	const body = "Routing decision.\n"
	tests := []struct {
		name, file string
		wantOK     bool
	}{
		{"valid", "---\nrole: lead\nstatus: done\nattempt: 1\nworker: engineer\n---\n" + body, true},
		{"scalar next", "---\nrole: lead\nstatus: done\nattempt: 1\nnext: triage:engineer\nworker: engineer\n---\n" + body, false},
		{"verdict", "---\nrole: lead\nstatus: done\nattempt: 1\nverdict: approve\nworker: engineer\n---\n" + body, false},
		{"extra key", "---\nrole: lead\nstatus: done\nattempt: 1\nrouting: x\nworker: engineer\n---\n" + body, false},
		{"quoted attempt", "---\nrole: lead\nstatus: done\nattempt: \"1\"\nworker: engineer\n---\n" + body, false},
		{"wrong role", "---\nrole: triage/lead\nstatus: done\nattempt: 1\nworker: engineer\n---\n" + body, false},
		{"empty body", "---\nrole: lead\nstatus: done\nattempt: 1\nworker: engineer\n---\n", false},
		{"leading blank line", "\n---\nrole: lead\nstatus: done\nattempt: 1\nworker: engineer\n---\n" + body, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rf, err := closeout.Parse([]byte(tc.file))
			if tc.wantOK {
				if err != nil || rf.Role != "lead" || rf.Verdict != "" {
					t.Fatalf("control did not parse cleanly: %+v, %v", rf, err)
				}
				return
			}
			if err != nil {
				return
			}
			if rf.Role == "lead" && rf.Verdict == "" {
				t.Errorf("parsed and passes the role/verdict checks: %+v", rf)
			}
		})
	}
}
