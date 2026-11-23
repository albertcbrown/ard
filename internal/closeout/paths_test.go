package closeout

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/redact"
)

func ruleMap(rs []redact.PathRule) map[string]string {
	m := map[string]string{}
	for _, r := range rs {
		m[r.From] = r.Name + "=" + r.To
	}
	return m
}

func TestPathRules(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "home", ".ard", "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	rr, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(alias, "home")
	host := HostPaths{Home: home, ArdRoot: filepath.Join(home, ".ard"), TempDir: filepath.Join(alias, "tmp") + "/"}
	gone := filepath.Join(alias, "home", ".ard", "work", "o", "r", "JOB") // never created
	jobHome := filepath.Join(home, ".ard", "work", "o", "r", "JOB.home")

	m := ruleMap(PathRules(gone, jobHome, host))
	for from, want := range map[string]string{
		gone: "path.work=/work",
		filepath.Join(rr, "home/.ard/work/o/r/JOB"): "path.work=/work",
		jobHome:                        "path.job-home=/ard-home",
		host.ArdRoot:                   "path.ard-root=~/.ard",
		filepath.Join(rr, "home/.ard"): "path.ard-root=~/.ard",
		home:                           "path.home=~",
		filepath.Join(rr, "home"):      "path.home=~",
		filepath.Join(alias, "tmp"):    "path.tmp=$TMPDIR",
		filepath.Join(rr, "tmp"):       "path.tmp=$TMPDIR",
	} {
		if m[from] != want {
			t.Errorf("rule for %s = %q, want %q", from, m[from], want)
		}
	}

	if got := PathRules("", "relative/x", HostPaths{Home: "/", ArdRoot: ""}); len(got) != 0 {
		t.Errorf("empty, relative and / must be skipped: %v", got)
	}
	for _, tmp := range []string{"/tmp", "/tmp/", "/var/tmp", "/private/tmp"} {
		for _, r := range PathRules("", "", HostPaths{TempDir: tmp}) {
			t.Errorf("TempDir %q gave a rule: %v", tmp, r.Name)
		}
	}
	// A temp dir equal to the home alias is a duplicate, not a second rule.
	n := 0
	for _, r := range PathRules("", "", HostPaths{Home: home, TempDir: home}) {
		if r.From == home {
			n++
		}
	}
	if n != 1 {
		t.Errorf("duplicate From emitted %d times", n)
	}
}

func TestPrepareReplacesHostPaths(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "done"
		if failed {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			host := HostPaths{Home: home, ArdRoot: filepath.Join(home, ".ard")}
			e := newEnv(t)
			e.d.NewRedactor = JobRedactor(func() []string { return nil }, host)
			if err := os.MkdirAll(host.ArdRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			j := e.claim(StageTriage, "lead")
			j.Home = filepath.Join(host.ArdRoot, "work", "o", "r", j.ID+".home")
			j.Worktree = e.repo
			rwt, _ := filepath.EvalSymlinks(e.repo)
			rhome, _ := filepath.EvalSymlinks(home)
			from := []string{e.repo, rwt, j.Home, filepath.Join(j.Home, ".claude"), host.ArdRoot, home, rhome}
			log := ""
			for _, p := range from {
				log += `{"type":"system","cwd":"` + p + `"}` + "\n"
				log += `{"cmd":"cd ` + p + `; cat .ard-input/issue.md"}` + "\n"
			}
			log += "key=" + secretValue + "\n"
			if err := os.WriteFile(j.LogPath, []byte(log), 0o600); err != nil {
				t.Fatal(err)
			}
			e.write(".ard/7/triage-lead.md", roleFile("lead", "done")+"Worked in "+e.repo+"/src\n")
			if failed {
				j.ExitCode = 1
			}
			rec, err := Prepare(context.Background(), j, e.d)
			if err != nil {
				t.Fatal(err)
			}
			var all []string
			for _, f := range rec.Plan.Files {
				b, err := os.ReadFile(f.Staged)
				if err != nil {
					t.Fatal(err)
				}
				all = append(all, string(b))
			}
			all = append(all, rec.Plan.Tail, rec.Plan.Summary, rec.Plan.Reason)
			joined := strings.Join(all, "\n")
			for _, p := range from {
				if strings.Contains(joined, p) {
					t.Errorf("host path %s survives in staged bytes, tail, summary or reason", p)
				}
			}
			for _, want := range []string{"/work", "~/.ard", "~", "/ard-home/.claude"} {
				if !strings.Contains(staged(t, rec.Plan, logPath), want) {
					t.Errorf("log lacks %q", want)
				}
			}
			if !failed && !strings.Contains(joined, "Worked in /work/src") {
				t.Errorf("role file path not replaced: %q", staged(t, rec.Plan, ".ard/7/triage-lead.md"))
			}
			if failed && !strings.Contains(rec.Plan.Tail, "~/.ard") {
				t.Errorf("tail = %q", rec.Plan.Tail)
			}

			// The posted comment, through a full close-out.
			e2 := newEnv(t)
			e2.d.NewRedactor = e.d.NewRedactor
			j2 := e2.claim(StageTriage, "lead")
			j2.Home = filepath.Join(host.ArdRoot, "work", "o", "r", j2.ID+".home")
			if err := os.WriteFile(j2.LogPath, []byte(log), 0o600); err != nil {
				t.Fatal(err)
			}
			e2.write(".ard/7/triage-lead.md", roleFile("lead", "done")+"Worked in "+e2.repo+"/src\n")
			if failed {
				j2.ExitCode = 1
			}
			if _, err := New(e2.d).Run(context.Background(), j2); err != nil {
				t.Fatal(err)
			}
			cs, err := e2.gh.ListComments(context.Background(), 7, time.Time{})
			if err != nil || len(cs) == 0 {
				t.Fatalf("comments = %v, %v", cs, err)
			}
			for _, c := range cs {
				for _, p := range []string{e2.repo, host.ArdRoot, home, rhome} {
					if strings.Contains(c.Body, p) {
						t.Errorf("comment holds %s: %q", p, c.Body)
					}
				}
			}
		})
	}
}

func TestJobRedactorBadRuleFailsClosed(t *testing.T) {
	// A newline in a path is a bad rule: the redactor must not be built.
	_, err := JobRedactor(func() []string { return nil }, HostPaths{Home: "/Users/zed\nx"})(Job{})
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "zed") {
		t.Errorf("error leaks the path: %v", err)
	}
}

func TestJobHomeRecordCompat(t *testing.T) {
	b, err := json.Marshal(Record{Version: recordVersion, Job: Job{ID: "J", Home: "/h"}})
	if err != nil {
		t.Fatal(err)
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil || r.Job.Home != "/h" {
		t.Errorf("round trip: %+v, %v", r.Job, err)
	}
	var old Record
	if err := json.Unmarshal([]byte(`{"Version":1,"Job":{"ID":"J","Worktree":"/w"}}`), &old); err != nil || old.Job.Home != "" || old.Job.Worktree != "/w" {
		t.Errorf("old record: %+v, %v", old.Job, err)
	}
}
