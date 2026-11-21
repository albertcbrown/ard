package botcheck_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

const workflowPath = "../../.github/workflows/bot-commits.yml"

var bot = github.Identity{Login: "ard-test[bot]", ID: 1000, Kind: github.AuthApp}

func loadYAML(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return m
}

func as[T any](t *testing.T, v any, what string) T {
	t.Helper()
	x, ok := v.(T)
	if !ok {
		t.Fatalf("%s: unexpected %T", what, v)
	}
	return x
}

func steps(t *testing.T, wf map[string]any) []any {
	jobs := as[map[string]any](t, wf["jobs"], "jobs")
	check := as[map[string]any](t, jobs["check"], "jobs.check")
	return as[[]any](t, check["steps"], "steps")
}

func script(t *testing.T) string {
	t.Helper()
	for _, s := range steps(t, loadYAML(t, workflowPath)) {
		m := as[map[string]any](t, s, "step")
		if m["id"] == "check" {
			run := as[string](t, m["run"], "run")
			if strings.Contains(run, "${{") {
				t.Fatal("run: must not contain ${{ }}; pass inputs through env")
			}
			return run
		}
	}
	t.Fatal("step id: check not found")
	return ""
}

func git(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type ident struct{ name, email string }

var (
	human   = ident{"Test", "test@example.com"}
	botID   = ident{bot.Login, bot.NoreplyEmail()}
	githubC = ident{"GitHub", "noreply@github.com"}
)

type entry struct {
	mode, path string
	del        bool
}

func reg(p string) entry { return entry{mode: "100644", path: p} }

// plant commits entries in dir with the given author and committer.
func plant(t *testing.T, dir string, author, committer ident, subject string, es ...entry) string {
	t.Helper()
	env := []string{
		"GIT_AUTHOR_NAME=" + author.name, "GIT_AUTHOR_EMAIL=" + author.email,
		"GIT_COMMITTER_NAME=" + committer.name, "GIT_COMMITTER_EMAIL=" + committer.email,
	}
	for _, e := range es {
		switch {
		case e.del:
			git(t, dir, env, "update-index", "--force-remove", "--", e.path)
		case e.mode == "160000":
			git(t, dir, env, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("a", 40)+","+e.path)
		default:
			cmd := exec.Command("git", "hash-object", "-w", "--stdin")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
			cmd.Stdin = strings.NewReader("x\n")
			oid, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			git(t, dir, env, "update-index", "--add", "--cacheinfo", e.mode+","+strings.TrimSpace(string(oid))+","+e.path)
		}
	}
	git(t, dir, env, "commit", "--allow-empty", "-m", subject)
	return git(t, dir, env, "rev-parse", "HEAD")
}

func botPlant(t *testing.T, dir string, es ...entry) string {
	return plant(t, dir, botID, botID, "ard(triage:lead): #12 x [skip ci]", es...)
}

func push(t *testing.T, clone string) {
	t.Helper()
	git(t, clone, nil, "push", "origin", "HEAD:main")
	git(t, clone, nil, "fetch", "origin")
}

func runCheck(t *testing.T, dir string, env map[string]string) (int, string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	cmd := exec.Command("bash", "-c", script(t))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if _, ok := env["GITHUB_STEP_SUMMARY"]; !ok {
		cmd.Env = append(cmd.Env, "GITHUB_STEP_SUMMARY=")
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &ee):
		return ee.ExitCode(), string(out)
	}
	t.Fatal(err)
	return -1, ""
}

func defEnv() map[string]string {
	return map[string]string{"BOT_LOGIN": bot.Login, "BRANCH": "main"}
}

func TestScript(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, clone, bare string) map[string]string // extra env
		code  int
		want  []string
	}{
		{"planted bot commit touches README", func(t *testing.T, c, _ string) map[string]string {
			botPlant(t, c, reg(".ard/12/plan.md"), reg("README.md"))
			push(t, c)
			return nil
		}, 1, []string{"README.md", "outside .ard/<issue#>/"}},
		{"workflow file", func(t *testing.T, c, _ string) map[string]string {
			botPlant(t, c, reg(".github/workflows/ci.yml"))
			push(t, c)
			return nil
		}, 1, []string{".github/workflows/ci.yml"}},
		{"config and role dir in separate commits", func(t *testing.T, c, _ string) map[string]string {
			botPlant(t, c, reg(".ard/config.yaml"))
			botPlant(t, c, reg(".ard/scanners/cve/scan"))
			push(t, c)
			return nil
		}, 1, []string{".ard/config.yaml", ".ard/scanners/cve/scan", "2 violation"}},
		{"two issues in one commit", func(t *testing.T, c, _ string) map[string]string {
			botPlant(t, c, reg(".ard/12/a"), reg(".ard/13/b"))
			push(t, c)
			return nil
		}, 1, []string{".ard/13/b", "also touches issue 12"}},
		{"bot may touch one issue under any subject", func(t *testing.T, c, _ string) map[string]string {
			plant(t, c, botID, botID, "chore: tidy", reg(".ard/12/a"))
			push(t, c)
			return nil
		}, 0, []string{"checked 1 bot commit"}},
		{"deletion is fine", func(t *testing.T, c, _ string) map[string]string {
			botPlant(t, c, reg(".ard/12/old.md"))
			botPlant(t, c, entry{path: ".ard/12/old.md", del: true})
			push(t, c)
			return nil
		}, 0, []string{"checked 2 bot commit"}},
		{"symlink", func(t *testing.T, c, _ string) map[string]string {
			botPlant(t, c, entry{mode: "120000", path: ".ard/12/link"})
			push(t, c)
			return nil
		}, 1, []string{".ard/12/link", "unsupported mode 120000"}},
		{"gitlink", func(t *testing.T, c, _ string) map[string]string {
			botPlant(t, c, entry{mode: "160000", path: ".ard/12/sub"})
			push(t, c)
			return nil
		}, 1, []string{".ard/12/sub", "unsupported mode 160000"}},
		{"executable is fine", func(t *testing.T, c, _ string) map[string]string {
			botPlant(t, c, entry{mode: "100755", path: ".ard/12/run.sh"})
			push(t, c)
			return nil
		}, 0, nil},
		{"human commit", func(t *testing.T, c, _ string) map[string]string {
			plant(t, c, human, human, "docs", reg("README.md"))
			push(t, c)
			return nil
		}, 0, []string{"checked 0 bot commit"}},
		{"squash: bot author, GitHub committer", func(t *testing.T, c, _ string) map[string]string {
			plant(t, c, botID, githubC, "ard(worker:coder): #12 fix (#99)", reg("main.go"))
			push(t, c)
			return nil
		}, 0, []string{"checked 0 bot commit"}},
		{"merge of a branch holding a bot commit", func(t *testing.T, c, _ string) map[string]string {
			git(t, c, nil, "checkout", "-q", "-b", "pr")
			botPlant(t, c, reg("main.go"))
			git(t, c, nil, "checkout", "-q", "main")
			git(t, c, []string{"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com"},
				"merge", "--no-ff", "-m", "merge pr", "pr")
			push(t, c)
			return nil
		}, 0, []string{"checked 0 bot commit"}},
		{"bot-committed merge commit", func(t *testing.T, c, _ string) map[string]string {
			git(t, c, nil, "checkout", "-q", "-b", "pr")
			plant(t, c, human, human, "side", reg("side.txt"))
			git(t, c, nil, "checkout", "-q", "main")
			plant(t, c, human, human, "main side", reg("main.txt"))
			git(t, c, []string{"GIT_AUTHOR_NAME=" + bot.Login, "GIT_AUTHOR_EMAIL=" + botID.email, "GIT_COMMITTER_NAME=" + bot.Login, "GIT_COMMITTER_EMAIL=" + botID.email},
				"merge", "--no-ff", "-m", "merge pr", "pr")
			push(t, c)
			return nil
		}, 1, []string{"merge commit"}},
		{"bot name with other email", func(t *testing.T, c, _ string) map[string]string {
			plant(t, c, human, ident{bot.Login, "x@example.com"}, "x", reg("README.md"))
			push(t, c)
			return nil
		}, 1, []string{"README.md"}},
		{"bot email with other name", func(t *testing.T, c, _ string) map[string]string {
			plant(t, c, human, ident{"Someone", botID.email}, "x", reg("README.md"))
			push(t, c)
			return nil
		}, 1, []string{"README.md"}},
		{"decoy emails are not the bot", func(t *testing.T, c, _ string) map[string]string {
			plant(t, c, human, ident{"Someone", "1000+ard-testXbot]@users.noreply.github.com"}, "x", reg("README.md"))
			plant(t, c, human, ident{"Someone", "abc+ard-test[bot]@users.noreply.github.com"}, "x", reg("README.md"))
			push(t, c)
			return nil
		}, 0, []string{"checked 0 bot commit"}},
		{"baseline at the violation", func(t *testing.T, c, _ string) map[string]string {
			v := botPlant(t, c, reg("README.md"))
			push(t, c)
			return map[string]string{"SINCE": v}
		}, 0, []string{"checked 0 bot commit"}},
		{"violation after the baseline", func(t *testing.T, c, _ string) map[string]string {
			ok := plant(t, c, human, human, "ok", reg("ok.txt"))
			botPlant(t, c, reg("README.md"))
			push(t, c)
			return map[string]string{"SINCE": ok}
		}, 1, []string{"README.md"}},
		{"only the last LIMIT commits are scanned", func(t *testing.T, c, _ string) map[string]string {
			botPlant(t, c, reg("README.md"))
			plant(t, c, human, human, "a", reg("a.txt"))
			plant(t, c, human, human, "b", reg("b.txt"))
			push(t, c)
			return map[string]string{"LIMIT": "2"}
		}, 0, []string{"checked 0 bot commit", "last 2"}},
		{"unknown baseline", func(t *testing.T, c, _ string) map[string]string {
			return map[string]string{"SINCE": strings.Repeat("1", 40)}
		}, 2, []string{"not a commit"}},
		{"baseline not on main", func(t *testing.T, c, _ string) map[string]string {
			git(t, c, nil, "checkout", "-q", "-b", "side")
			s := plant(t, c, human, human, "side", reg("s.txt"))
			git(t, c, nil, "push", "origin", "side")
			git(t, c, nil, "fetch", "origin")
			git(t, c, nil, "checkout", "-q", "main")
			return map[string]string{"SINCE": s}
		}, 2, []string{"not an ancestor"}},
		{"bot login unset", func(t *testing.T, c, _ string) map[string]string {
			return map[string]string{"BOT_LOGIN": ""}
		}, 2, []string{"ARD_BOT_LOGIN"}},
		{"shallow clone", func(t *testing.T, c, bare string) map[string]string {
			botPlant(t, c, reg("README.md"))
			push(t, c)
			plant(t, c, human, human, "more", reg("m.txt"))
			push(t, c)
			sh := filepath.Join(t.TempDir(), "shallow")
			git(t, filepath.Dir(sh), nil, "clone", "-q", "--depth", "1", "file://"+bare, sh)
			return map[string]string{"DIR": sh}
		}, 2, []string{"shallow"}},
		{"bot root commit", func(t *testing.T, c, _ string) map[string]string {
			git(t, c, nil, "checkout", "-q", "--orphan", "fresh")
			git(t, c, nil, "rm", "-rqf", "--ignore-unmatch", ".")
			botPlant(t, c, reg("README.md"))
			git(t, c, nil, "push", "-f", "origin", "HEAD:main")
			git(t, c, nil, "fetch", "origin")
			return nil
		}, 1, []string{"README.md"}},
		{"path cannot inject workflow commands", func(t *testing.T, c, _ string) map[string]string {
			botPlant(t, c, reg(".ard/12/a\n::add-mask::x"))
			push(t, c)
			return nil
		}, 1, []string{".ard/12/a%0A::add-mask::x"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bare, clone := gittest.NewRemote(t)
			extra := tc.setup(t, clone, bare)
			env := defEnv()
			dir := clone
			for k, v := range extra {
				if k == "DIR" {
					dir = v
					continue
				}
				env[k] = v
			}
			code, out := runCheck(t, dir, env)
			if code != tc.code {
				t.Fatalf("exit %d, want %d\n%s", code, tc.code, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q\n%s", w, out)
				}
			}
		})
	}
}

func TestInjectionYieldsOneErrorLine(t *testing.T) {
	_, clone := gittest.NewRemote(t)
	botPlant(t, clone, reg(".ard/12/a\n::add-mask::x"))
	push(t, clone)
	_, out := runCheck(t, clone, defEnv())
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "::") {
			n++
			if !strings.HasPrefix(l, "::error ") {
				t.Errorf("unexpected command line %q", l)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d workflow command lines, want 1\n%s", n, out)
	}
}

func TestStepSummary(t *testing.T) {
	_, clone := gittest.NewRemote(t)
	sha := botPlant(t, clone, reg("a|b`c.txt"))
	push(t, clone)
	sum := filepath.Join(t.TempDir(), "summary.md")
	env := defEnv()
	env["GITHUB_STEP_SUMMARY"] = sum
	if code, out := runCheck(t, clone, env); code != 1 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	b, err := os.ReadFile(sum)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), sha) || !strings.Contains(string(b), "a\\|b\\`c.txt") {
		t.Fatalf("summary:\n%s", b)
	}
}

// The check must recognise what the real main-lane committer writes.
func TestRealCommitter(t *testing.T) {
	bare, clone := gittest.NewRemote(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	g := gitx.New("")
	c, err := committer.NewMain(context.Background(), committer.Config{
		RepoDir: clone, WorkDir: t.TempDir(), RemoteURL: "file://" + bare, Bot: bot,
		Backoff: func(int) time.Duration { return 0 }, Log: log,
	}, effects.NewLive(fake.New(), g, log), g)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	req := committer.Request{Issue: 12, Stage: "triage", Role: "lead", Summary: "plan", JobID: "01JABC",
		Files: []committer.File{{Path: ".ard/12/plan.md", Data: []byte("x\n")}}}
	if _, err := c.Commit(ctx, req); err != nil {
		t.Fatal(err)
	}
	git(t, clone, nil, "fetch", "origin")
	code, out := runCheck(t, clone, defEnv())
	if code != 0 || !strings.Contains(out, "checked 1 bot commit") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// A planted out-of-bounds commit on top is caught.
	git(t, clone, nil, "merge", "-q", "--ff-only", "origin/main")
	botPlant(t, clone, reg("README.md"))
	push(t, clone)
	if code, out := runCheck(t, clone, defEnv()); code != 1 || !strings.Contains(out, "README.md") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// The script flags a path exactly when the committer's own guard rejects it.
func TestParityWithCommitterGuard(t *testing.T) {
	issueRe := regexp.MustCompile(`^\.ard/([1-9][0-9]*)/`)
	paths := []string{
		".ard/12/a.md", ".ard/12/logs/x.log", ".ard/12", ".ard/012/a", ".ard/0/a",
		".ard/12/.git/x", ".ard/12/.GIT/x", `.ard/12/a\b`, ".ard/12/a\tb", ".ard/12/a\nb",
		".ard/1/a", ".ard/config.yaml", "x/.ard/12/a", ".ard/12/./a", ".ard/12/../13/a",
	}
	for _, p := range paths {
		t.Run(strconv.Quote(p), func(t *testing.T) {
			_, clone := gittest.NewRemote(t)
			issue := 12
			if m := issueRe.FindStringSubmatch(p); m != nil {
				issue, _ = strconv.Atoi(m[1])
			}
			git(t, clone, nil, "hash-object", "-w", "--stdin")
			cmd := exec.Command("git", "update-index", "--add", "--cacheinfo",
				"100644,e69de29bb2d1d6434b8b29ae775ad8c2e48c5391,"+p)
			cmd.Dir = clone
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Logf("git refuses %q, row dropped: %s", p, strings.TrimSpace(string(out)))
				t.Skip("git cannot record this path")
			}
			env := []string{"GIT_AUTHOR_NAME=" + bot.Login, "GIT_AUTHOR_EMAIL=" + botID.email,
				"GIT_COMMITTER_NAME=" + bot.Login, "GIT_COMMITTER_EMAIL=" + botID.email}
			git(t, clone, env, "commit", "-m", "ard(triage:lead): #12 x")
			push(t, clone)
			code, out := runCheck(t, clone, defEnv())
			wantFlag := committer.CheckPath(issue, p) != nil
			if (code == 1) != wantFlag || (code != 0 && code != 1) {
				t.Fatalf("exit %d, CheckPath(%d) rejects = %v\n%s", code, issue, wantFlag, out)
			}
		})
	}
}

func TestWorkflowShape(t *testing.T) {
	wf := loadYAML(t, workflowPath)
	on := as[map[string]any](t, wf["on"], "on")
	push := as[map[string]any](t, on["push"], "on.push")
	if b := as[[]any](t, push["branches"], "branches"); len(b) != 1 || b[0] != "main" {
		t.Errorf("push branches = %v", b)
	}
	for _, k := range []string{"schedule", "workflow_dispatch"} {
		if _, ok := on[k]; !ok {
			t.Errorf("missing trigger %s", k)
		}
	}
	jobs := as[map[string]any](t, wf["jobs"], "jobs")
	check := as[map[string]any](t, jobs["check"], "jobs.check")
	perms := as[map[string]any](t, check["permissions"], "permissions")
	if perms["contents"] != "read" || len(perms) != 1 {
		t.Errorf("job permissions = %v", perms)
	}
	var found bool
	for _, s := range steps(t, wf) {
		m := as[map[string]any](t, s, "step")
		if u, _ := m["uses"].(string); strings.HasPrefix(u, "actions/checkout@") {
			w := as[map[string]any](t, m["with"], "with")
			found = w["fetch-depth"] == 0 && w["persist-credentials"] == false
		}
		if m["id"] == "check" {
			env := as[map[string]any](t, m["env"], "env")
			for k, v := range map[string]string{"BOT_LOGIN": "vars.ARD_BOT_LOGIN", "SINCE": "vars.ARD_BOT_CHECK_SINCE"} {
				if !strings.Contains(fmt.Sprint(env[k]), v) {
					t.Errorf("env %s = %v", k, env[k])
				}
			}
		}
	}
	if !found {
		t.Error("checkout must use fetch-depth: 0 and persist-credentials: false")
	}
}
