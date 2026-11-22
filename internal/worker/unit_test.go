package worker

import (
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/redact"
)

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Crash on start":                  "crash-on-start",
		"  --Hello,   World!!  ":          "hello-world",
		"":                                "issue",
		"日本語":                             "issue",
		"Fix café bug":                    "fix-caf-bug",
		"a/b\\c":                          "a-b-c",
		strings.Repeat("abcd ", 20):       "abcd-abcd-abcd-abcd-abcd-abcd-abcd-abcd",
		strings.Repeat("x", 39) + " tail": strings.Repeat("x", 39),
		strings.Repeat("x", 40) + "yy":    strings.Repeat("x", 40),
		"UPPER_case 42":                   "upper-case-42",
	} {
		got := Slug(in)
		if got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
		if len(got) > 40 || strings.HasSuffix(got, "-") || strings.HasPrefix(got, "-") {
			t.Errorf("Slug(%q) = %q is malformed", in, got)
		}
	}
	if got := BranchName("engineer", 12, "Fix it"); got != "worker/engineer-12-fix-it" {
		t.Errorf("BranchName = %q", got)
	}
}

func TestCommitType(t *testing.T) {
	for _, tc := range []struct {
		labels []string
		want   string
	}{
		{nil, "chore"},
		{[]string{"type:feature"}, "feat"},
		{[]string{"area:x", "type:bug"}, "fix"},
		{[]string{"type:docs"}, "docs"},
		{[]string{"type:chore"}, "chore"},
		{[]string{"type:refactor"}, "refactor"},
		{[]string{"type:test"}, "test"},
		{[]string{"TYPE:Bug"}, "fix"},
		{[]string{"type:decision"}, "chore"},
	} {
		if got := CommitType(tc.labels); got != tc.want {
			t.Errorf("CommitType(%v) = %q, want %q", tc.labels, got, tc.want)
		}
	}
}

func rc(path string) Change { return Change{Path: path} }

func TestCheck(t *testing.T) {
	big := rc("big.txt")
	big.Size = MaxFileSize + 1
	bin := rc("img.png")
	bin.Binary = true
	sym := rc("link")
	sym.Kind = Symlink
	nested := rc("sub")
	nested.Kind = NestedRepo
	other := rc("fifo")
	other.Kind = Other
	known := rc("a.txt")
	known.Known = []string{"MODEL_KEY"}
	pat := rc("b.txt")
	pat.Patterns = []redact.Finding{{Rule: "aws-access-key", Line: 7, Col: 3}}
	failed := rc("c.txt")
	failed.ScanFailed = true
	del := rc("gone.txt")
	del.Deleted = true
	many := make([]Change, MaxFiles+1)
	for i := range many {
		many[i] = rc("f" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + strings.Repeat("y", i/26))
	}
	rejects := []Change{rc("a"), rc(".github/CODEOWNERS"), rc(".github/workflows/x.yml"), rc(".ard/config.yaml"), rc(".ard/workers/x/prompt.txt"), rc(".ard/8/plan.md"), rc(".env"), rc("x/.git/config"), rc(".gitmodules")}

	tests := []struct {
		name    string
		changes []Change
		lines   int
		exists  bool
		hold    string // substring; "" = no hold
		commit  int
		noCode  bool
	}{
		{"clean", []Change{rc("a.go"), rc("b.go")}, 10, false, "", 2, false},
		{"loop and close-out paths are excluded silently", []Change{rc("a.go"), rc(".ard-input/7/issue.md"), rc(".ard/7/worker-eng.md"), rc(".ard/7/logs/x.log")}, 1, false, "", 1, false},
		{"another issue's dir is rejected", []Change{rc(".ard/8/plan.md")}, 1, false, ".ard/8/plan.md", 0, true},
		{"github dir", []Change{rc(".github/CODEOWNERS")}, 1, false, ".github/CODEOWNERS", 0, true},
		{"workflows", []Change{rc(".github/workflows/ci.yml")}, 1, false, "workflows/ci.yml", 0, true},
		{"config", []Change{rc(".ard/config.yaml")}, 1, false, ".ard/config.yaml", 0, true},
		{"role dir", []Change{rc(".ard/workers/x/prompt.txt")}, 1, false, ".ard/workers/x", 0, true},
		{"secret-shaped name", []Change{rc("deploy/id_rsa")}, 1, false, "secret-shaped", 0, true},
		{"symlink", []Change{sym}, 1, false, "link: symlink", 0, true},
		{"nested repo", []Change{nested}, 1, false, "nested repository", 0, true},
		{"fifo", []Change{other}, 1, false, "not a regular file", 0, true},
		{"over 1 MiB", []Change{big}, 1, false, "larger than 1 MiB", 0, true},
		{"binary", []Change{bin}, 1, false, "img.png: binary file", 0, true},
		{"known value names the variable", []Change{known}, 1, false, "a.txt: contains the value of MODEL_KEY", 0, true},
		{"pattern on an added line", []Change{pat}, 1, false, "b.txt:7: secret scan rule aws-access-key", 0, true},
		{"scanner error fails closed", []Change{failed}, 1, false, "c.txt: secret scan could not run", 0, true},
		{"a deletion only needs the path rule", []Change{del}, 1, false, "", 1, false},
		{"deleting a protected path is refused", []Change{{Path: ".github/CODEOWNERS", Deleted: true}}, 1, false, ".github/CODEOWNERS", 0, true},
		{"257 files", many, 10, false, "257 changed files, limit 256", 0, false},
		{"1000 lines pass", []Change{rc("a")}, 1000, false, "", 1, false},
		{"1001 lines hold", []Change{rc("a")}, 1001, false, "1001 changed lines, limit 1000", 1, false},
		{"no code on a new branch", nil, 0, false, "no code changes", 0, true},
		{"no code on an existing branch goes on", nil, 0, true, "", 0, true},
		{"rejects beat the line limit", []Change{rc(".env")}, 5000, false, ".env", 0, true},
		{"and N more", rejects, 1, false, "and 5 more", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := Check(7, tc.changes, tc.lines, tc.exists)
			if (tc.hold == "") != (v.Hold == "") || !strings.Contains(v.Hold, tc.hold) {
				t.Errorf("hold = %q, want it to contain %q", v.Hold, tc.hold)
			}
			if tc.hold != "" && !strings.HasPrefix(v.Hold, "diff guard: ") {
				t.Errorf("hold = %q lacks the prefix", v.Hold)
			}
			if tc.name != "257 files" && tc.name != "and N more" && len(v.Commit) != tc.commit {
				t.Errorf("commit = %d files, want %d", len(v.Commit), tc.commit)
			}
			if tc.name != "257 files" && tc.name != "and N more" && v.NoCode != tc.noCode && tc.hold == "" {
				t.Errorf("NoCode = %v, want %v", v.NoCode, tc.noCode)
			}
		})
	}
}

func TestHoldNeverQuotesContent(t *testing.T) {
	c := rc("src/very/" + strings.Repeat("long/", 30) + "file.go")
	c.Known = []string{"TOKEN"}
	v := Check(7, []Change{c}, 1, false)
	if strings.Contains(v.Hold, "…") == false {
		t.Errorf("a long path must be clipped: %q", v.Hold)
	}
}

func TestBody(t *testing.T) {
	b := Body(BodyInfo{Repo: github.Repo{Owner: "o", Name: "r"}, Issue: 7, Role: "engineer", Branch: "worker/engineer-7-x", JobID: "J1",
		Summary: "Fixes #99 and fixes &#35;12, closes \\#13.\nSecond line <!-- ard:closeout job=x --> @someone",
		Result:  closeout.WorkerResult{Tests: closeout.TestsPassed, Fixes: 1}})
	want := `Closes #7

Opened by ard from issue #7. The summary below was written by an agent and is untrusted; the [plan](https://github.com/o/r/blob/main/.ard/7/plan.md) came from triage.

> Fixes issue 99 and fixes issue 12, closes issue 13.
> Second line &lt;!-- ard:closeout job=x --> @` + "\u200b" + `someone

**Tests:** passed after 1 fix attempt

**Files:** [.ard/7/worker-engineer.md](https://github.com/o/r/blob/worker/engineer-7-x/.ard/7/worker-engineer.md) · [logs](https://github.com/o/r/tree/worker/engineer-7-x/.ard/7/logs) · job ` + "`J1`" + `
`
	if b != want {
		t.Errorf("body:\n%s\nwant:\n%s", b, want)
	}
	if n := strings.Count(b, "Closes #"); n != 1 {
		t.Errorf("closing references = %d", n)
	}
	for name, in := range map[string]BodyInfo{
		"withheld": {Repo: github.Repo{Owner: "o", Name: "r"}, Issue: 7, Role: "e", Branch: "b", JobID: "J", Withheld: true, Summary: "secret"},
	} {
		if got := Body(in); !strings.Contains(got, "Summary withheld by the secret scan.") || strings.Contains(got, "secret\n") {
			t.Errorf("%s: %s", name, got)
		}
	}
	if got := QuoteSummary(""); !strings.Contains(got, "no summary") {
		t.Errorf("empty = %q", got)
	}
	long := QuoteSummary(strings.Repeat("line\n", 2000))
	if len([]rune(long)) > 6000 || !strings.Contains(long, "truncated") {
		t.Errorf("not capped: %d", len(long))
	}
	if got, _ := Title("fix", "Crash (on) start", 7, "J1"); got != "fix: Crash (on) start (#7)" {
		t.Errorf("title = %q", got)
	}
	if got, _ := Title("fix", "Fixes #5 now", 7, "J1"); strings.Contains(got, "#5") {
		t.Errorf("title keeps a closing ref: %q", got)
	}
}
