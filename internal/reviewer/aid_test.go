package reviewer

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/redact"
)

// withAid gives the reviewer role an aid script and the PR a changed a.go.
func withAid(h *harness) {
	for i := range h.cfg.Roles {
		if h.cfg.Roles[i].Name == reviewer {
			h.cfg.Roles[i].AidPath = h.cfg.Roles[i].Dir + "/aid"
		}
	}
	h.git.changed = []string{"a.go", "dir/b.go"}
}

func aidLines(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

const (
	goodSemgrep = `{"tool":"semgrep","rule":"r1","file":"a.go","line":1,"severity":"medium","message":"m1"}`
	goodGosec   = `{"tool":"gosec","rule":"G204","file":"dir/b.go","line":3,"severity":"high","message":"m2"}`
)

func aidFiles(ej executor.Job) map[string]string {
	m := map[string]string{}
	for _, f := range ej.Inputs {
		m[f.Path] = string(f.Data)
	}
	return m
}

func runAidScenario(t *testing.T, out string, mut ...func(*harness)) (*harness, int) {
	t.Helper()
	h := newHarness(t, withAid)
	for _, m := range mut {
		m(h)
	}
	h.ex.aidOut = out
	_, issue := h.scenario()
	h.mustTick()
	return h, issue
}

func TestAidOutputReachesAgent(t *testing.T) {
	h, issue := runAidScenario(t, aidLines(goodSemgrep, goodGosec))
	if len(h.ex.prepared) != 2 || len(h.ex.started) != 2 {
		t.Fatalf("prepared %d started %d, want the aid job and the agent job", len(h.ex.prepared), len(h.ex.started))
	}
	dir := fmt.Sprintf(".ard-input/%d/", issue)
	// The aid job: read-only, no network, no credentials, pinned tip.
	aj := h.ex.prepared[0]
	if !aj.ReadOnly || aj.Network != executor.NetworkNone || aj.Ref != tipSHA || aj.Timeout != aidTimeout || !strings.HasSuffix(aj.ID, "a") {
		t.Errorf("aid job = %+v", aj)
	}
	af := aidFiles(aj)
	if af[dir+"changed.txt"] != "a.go\ndir/b.go\n" || !strings.Contains(af[dir+"diff.patch"], "a.go | 1 +") {
		t.Errorf("aid inputs = %v", af)
	}
	if c := h.ex.started[0]; len(c.Env) != 0 || len(c.Fixed) != 0 || c.Path != "/role/aid" {
		t.Errorf("aid command = %+v", c)
	}
	// The agent gets aid.jsonl as an untrusted input.
	files := aidFiles(h.ex.prepared[1])
	got := files[dir+"aid.jsonl"]
	if !strings.HasPrefix(got, "UNTRUSTED INPUT") || !strings.Contains(got, "2 results kept, 0 dropped") || !strings.Contains(got, goodSemgrep) || !strings.Contains(got, goodGosec) {
		t.Errorf("aid.jsonl = %q", got)
	}
	if argv := strings.Join(h.ex.started[1].Args, " "); !strings.Contains(argv, dir+"aid.jsonl") {
		t.Errorf("argv lacks aid.jsonl:\n%s", argv)
	}
	rc := h.fin.got()[0].Opts.Review
	if !rc.HasAid || rc.AidFailed != "" || len(rc.Injected) != 0 {
		t.Errorf("review context = %+v", rc)
	}
	if to := h.ex.prepared[1].Timeout; to < minAgentTime || to > 30*time.Minute {
		t.Errorf("agent timeout = %v", to)
	}
}

func TestAidInvalidLinesDroppedAndCounted(t *testing.T) {
	bad := []string{
		`not json`,
		`{"tool":"x","rule":"r","file":"a.go","line":1,"severity":"low","message":"m","secret":"s"}`,
		`{"tool":"x","rule":"r","file":"../x","line":1,"severity":"low","message":"m"}`,
		`{"tool":"x","rule":"r","file":"/etc/passwd","line":1,"severity":"low","message":"m"}`,
		`{"tool":"x","rule":"r","file":"other.go","line":1,"severity":"low","message":"m"}`,
		`{"tool":"x","rule":"r","file":"a.go","line":1,"severity":"urgent","message":"m"}`,
		`{"tool":"x","rule":"r","file":"a.go","line":-1,"severity":"low","message":"m"}`,
		`{"tool":"","rule":"r","file":"a.go","line":1,"severity":"low","message":"m"}`,
	}
	h, issue := runAidScenario(t, aidLines(append([]string{goodSemgrep}, bad...)...))
	got := aidFiles(h.ex.prepared[1])[fmt.Sprintf(".ard-input/%d/aid.jsonl", issue)]
	if !strings.Contains(got, fmt.Sprintf("1 results kept, %d dropped", len(bad))) {
		t.Errorf("aid.jsonl = %q", got)
	}
	for _, s := range []string{"../x", "/etc/passwd", "other.go", "urgent", `"secret"`} {
		if strings.Contains(got, s) {
			t.Errorf("aid.jsonl keeps %q", s)
		}
	}
	// Invalid output means the review cannot approve.
	if rc := h.fin.got()[0].Opts.Review; !strings.Contains(rc.AidFailed, "invalid output") {
		t.Errorf("AidFailed = %q", rc.AidFailed)
	}
}

func TestAidOutputOver256KiB(t *testing.T) {
	h, issue := runAidScenario(t, strings.Repeat(goodSemgrep+"\n", 4000))
	got := aidFiles(h.ex.prepared[1])[fmt.Sprintf(".ard-input/%d/aid.jsonl", issue)]
	if !strings.Contains(got, "The aid failed: output over 256 KiB") {
		t.Errorf("aid.jsonl = %q", got)
	}
	if len(h.ex.started) != 2 {
		t.Errorf("the review did not run: started %d", len(h.ex.started))
	}
}

func TestAidSecretInMessageIsRedacted(t *testing.T) {
	val := "zq" + strings.Repeat("x7", 12)
	line := fmt.Sprintf(`{"tool":"semgrep","rule":"r","file":"a.go","line":1,"severity":"low","message":"found %s here"}`, val)
	h, issue := runAidScenario(t, aidLines(line), func(h *harness) {
		h.loop.d.Secrets = func() *redact.Secrets {
			s := redact.NewSecrets()
			if err := s.Add("TEST_KEY", val); err != nil {
				t.Fatal(err)
			}
			return s
		}
	})
	got := aidFiles(h.ex.prepared[1])[fmt.Sprintf(".ard-input/%d/aid.jsonl", issue)]
	if strings.Contains(got, val) || !strings.Contains(got, "REDACTED") {
		t.Errorf("aid.jsonl = %q", got)
	}
}

func TestAidFailureStillRunsReview(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*harness)
		want string
	}{
		{"nonzero exit", func(h *harness) { h.ex.aidExit = 4 }, "exit status 4"},
		{"timeout", func(h *harness) { h.ex.aidState = executor.TimedOut }, "timed out"},
		{"prepare error", func(h *harness) { h.ex.aidErr = errBoom }, "could not prepare the aid job"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, issue := runAidScenario(t, aidLines(goodSemgrep), tc.mut)
			got := aidFiles(h.ex.prepared[len(h.ex.prepared)-1])[fmt.Sprintf(".ard-input/%d/aid.jsonl", issue)]
			if !strings.Contains(got, "The aid failed: ") || !strings.Contains(got, tc.want) || strings.Contains(got, goodSemgrep) {
				t.Errorf("aid.jsonl = %q", got)
			}
			if len(h.ex.started) == 0 || len(h.fin.got()) != 1 {
				t.Fatalf("the review did not run")
			}
			if rc := h.fin.got()[0].Opts.Review; !rc.HasAid || !strings.Contains(rc.AidFailed, tc.want) {
				t.Errorf("review context = %+v", rc)
			}
		})
	}
}

func TestNoAidNoFile(t *testing.T) {
	h := newHarness(t)
	_, issue := h.scenario()
	h.mustTick()
	if len(h.ex.prepared) != 1 {
		t.Fatalf("prepared %d jobs, want only the agent job", len(h.ex.prepared))
	}
	if _, ok := aidFiles(h.ex.prepared[0])[fmt.Sprintf(".ard-input/%d/aid.jsonl", issue)]; ok {
		t.Error("aid.jsonl written for a role without an aid")
	}
	if rc := h.fin.got()[0].Opts.Review; rc.HasAid || rc.AidFailed != "" {
		t.Errorf("review context = %+v", rc)
	}
}

func TestGitleaksOnAddedLineBlocks(t *testing.T) {
	changed := map[string]bool{"a.go": true}
	added := map[string][]gitx.LineRange{"a.go": {{From: 1, To: 2}}}
	mk := func(tool string, line int) string {
		return fmt.Sprintf(`{"tool":%q,"rule":"aws-access-token","file":"a.go","line":%d,"severity":"high","message":"m"}`, tool, line)
	}
	tests := []struct {
		name string
		line string
		want int
	}{
		{"gitleaks on an added line", mk("gitleaks", 2), 1},
		{"gitleaks on a context line", mk("gitleaks", 9), 0},
		{"gosec on an added line", mk("gosec", 2), 0},
		{"semgrep on an added line", mk("semgrep", 1), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, kept, dropped, blocking := validateAid(context.Background(), []byte(tc.line+"\n"), changed, added, nil)
			if kept != 1 || dropped != 0 || len(blocking) != tc.want {
				t.Fatalf("kept %d dropped %d blocking %+v", kept, dropped, blocking)
			}
			for _, f := range blocking {
				if !f.Blocking || f.Severity != "high" || f.File != "a.go" || f.Line != 2 || !strings.HasPrefix(f.Message, "[secret]") {
					t.Errorf("finding = %+v", f)
				}
			}
		})
	}
}

func TestValidateAidEdgeCases(t *testing.T) {
	changed := map[string]bool{"a.go": true, "my dir/b c.go": true}
	empty, kept, dropped, _ := validateAid(context.Background(), nil, changed, nil, nil)
	if len(empty) != 0 || kept != 0 || dropped != 0 {
		t.Errorf("empty input: %q %d %d", empty, kept, dropped)
	}
	dup := `{"tool":"semgrep","rule":"r","file":"my dir/b c.go","line":1,"severity":"low","message":"m"}`
	long := `{"tool":"semgrep","rule":"r","file":"a.go","line":1,"severity":"low","message":"` + strings.Repeat("x", 5000) + `"}`
	out, kept, dropped, _ := validateAid(context.Background(), []byte(dup+"\n"+dup+"\n"+long+"\n"+"\xff\xfe\n"), changed, nil, nil)
	if kept != 2 || dropped != 1 || len(out) > 2200 {
		t.Errorf("kept %d dropped %d len %d", kept, dropped, len(out))
	}
}
