package versioncheck_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	workflowPath = "../../.github/workflows/version.yml"
	repo         = "o/r"
	botLogin     = "ard-test[bot]"
	sha40        = "0123456789012345678901234567890123456789"
)

func loadYAML(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
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

// script returns the run: block of step id in job.
func script(t *testing.T, job, id string) string {
	t.Helper()
	jobs := as[map[string]any](t, loadYAML(t)["jobs"], "jobs")
	j := as[map[string]any](t, jobs[job], job)
	for _, s := range as[[]any](t, j["steps"], "steps") {
		m := as[map[string]any](t, s, "step")
		if m["id"] == id {
			run := as[string](t, m["run"], "run")
			if strings.Contains(run, "${{") {
				t.Fatal("run: must not contain ${{ }}; pass inputs through env")
			}
			return run
		}
	}
	t.Fatalf("step %s/%s not found", job, id)
	return ""
}

// bash4 finds a bash that supports declare -A.
func bash4(t *testing.T) string {
	t.Helper()
	for _, c := range []string{"bash", "/opt/homebrew/bin/bash", "/usr/local/bin/bash"} {
		p, err := exec.LookPath(c)
		if err != nil {
			continue
		}
		out, err := exec.Command(p, "-c", "echo ${BASH_VERSINFO[0]}").Output()
		if err == nil && strings.TrimSpace(string(out)) >= "4" {
			return p
		}
	}
	t.Skip("needs bash 4 or newer")
	return ""
}

func gitEnv(extra ...string) []string {
	return append(append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com"), extra...)
}

func git(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type ident struct{ name, email string }

var human = ident{"Test", "test@example.com"}

const fakeGH = `#!/bin/bash
# fake gh: "api PATH" reads $GHSTATE/api/<PATH with / as _>.json
case "$1" in
api)
  f="$GHSTATE/api/${2//\//_}.json"
  n=$(cat "$f.n" 2>/dev/null || echo 0)
  echo $((n + 1)) >"$f.n"
  if [[ -f "$f.err" ]]; then echo "gh: Server Error (HTTP 500)" >&2; exit 1; fi
  if [[ $n -eq 0 && -f "$f.first" ]]; then cat "$f.first"; exit 0; fi
  if [[ -f "$f" ]]; then cat "$f"; exit 0; fi
  echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;;
release)
  echo "$*" >>"$GHSTATE/release.log"
  [[ ! -f "$GHSTATE/release.fail" ]] || exit 1
  exit 0 ;;
esac
exit 1
`

type fx struct {
	t             *testing.T
	bash          string
	origin, clone string
	state         string
}

func newFx(t *testing.T, seedTag, gomod string) *fx {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("needs jq")
	}
	d := t.TempDir()
	f := &fx{t: t, bash: bash4(t), origin: filepath.Join(d, "origin.git"), clone: filepath.Join(d, "clone"),
		state: filepath.Join(d, "state")}
	if err := os.MkdirAll(filepath.Join(f.state, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(d, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGH), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GHSTATE", f.state)
	git(t, d, nil, "init", "-q", "--bare", "-b", "main", f.origin)
	git(t, d, nil, "init", "-q", "-b", "main", f.clone)
	git(t, f.clone, nil, "remote", "add", "origin", f.origin)
	f.setGoMod(gomod)
	f.commit("seed", human)
	if seedTag != "" {
		git(t, f.clone, nil, "tag", "-a", "-m", seedTag, seedTag)
		git(t, f.clone, nil, "push", "-q", "origin", "refs/tags/"+seedTag)
		f.ghJSON("repos/"+repo+"/releases/tags/"+seedTag, map[string]any{"tag_name": seedTag})
	}
	return f
}

func (f *fx) setGoMod(module string) {
	f.t.Helper()
	body := "module " + module + "\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(f.clone, "go.mod"), []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
	git(f.t, f.clone, nil, "add", "go.mod")
}

// commit makes a commit on main, pushes it and returns its sha.
func (f *fx) commit(subject string, c ident) string {
	f.t.Helper()
	git(f.t, f.clone, []string{"GIT_COMMITTER_NAME=" + c.name, "GIT_COMMITTER_EMAIL=" + c.email},
		"commit", "-q", "--allow-empty", "-m", subject)
	git(f.t, f.clone, nil, "push", "-q", "origin", "main")
	return git(f.t, f.clone, nil, "rev-parse", "HEAD")
}

// merge makes a --no-ff merge commit of a side branch.
func (f *fx) merge(subject string) string {
	f.t.Helper()
	git(f.t, f.clone, nil, "checkout", "-q", "-b", "side")
	git(f.t, f.clone, nil, "commit", "-q", "--allow-empty", "-m", "side work")
	git(f.t, f.clone, nil, "checkout", "-q", "main")
	git(f.t, f.clone, nil, "merge", "-q", "--no-ff", "-m", subject, "side")
	git(f.t, f.clone, nil, "branch", "-q", "-D", "side")
	git(f.t, f.clone, nil, "push", "-q", "origin", "main")
	return git(f.t, f.clone, nil, "rev-parse", "HEAD")
}

// ghFile writes a fake API answer; suffix is "" (normal), ".first" (first
// call only) or ".err" (a server error).
func (f *fx) ghFile(path, suffix, body string) {
	f.t.Helper()
	p := filepath.Join(f.state, "api", strings.ReplaceAll(path, "/", "_")+".json"+suffix)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) ghJSON(path string, v any) {
	f.t.Helper()
	b, _ := json.Marshal(v)
	f.ghFile(path, "", string(b))
}

// pr registers a merged PR for sha.
func (f *fx) pr(sha string, n int, title string, labels ...string) {
	f.t.Helper()
	ls := []map[string]string{}
	for _, l := range labels {
		ls = append(ls, map[string]string{"name": l})
	}
	p := map[string]any{"number": n, "title": title, "merged_at": "2026-01-01T00:00:00Z",
		"merge_commit_sha": sha, "base": map[string]string{"ref": "main"}, "labels": ls}
	f.ghJSON("repos/"+repo+"/commits/"+sha+"/pulls", []any{p})
	f.ghJSON("repos/"+repo+"/pulls/"+strconv.Itoa(n), p)
}

func (f *fx) noPRs(sha string) {
	f.ghJSON("repos/"+repo+"/commits/"+sha+"/pulls", []any{})
}

type result struct {
	code int
	out  string
	rows [][]string
	live string
	raw  []byte
	cnt  string
}

func (f *fx) run(script string, env map[string]string, out string) result {
	f.t.Helper()
	cmd := exec.Command(f.bash, "-c", script)
	cmd.Dir = f.clone
	cmd.Env = gitEnv("GITHUB_OUTPUT="+out, "GITHUB_STEP_SUMMARY="+filepath.Join(f.t.TempDir(), "sum"))
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		f.t.Fatal(err)
	}
	return result{code: code, out: buf.String()}
}

// plan runs the plan script after fetching origin.
func (f *fx) plan(env map[string]string) result {
	f.t.Helper()
	git(f.t, f.clone, nil, "fetch", "-q", "--tags", "origin")
	e := map[string]string{"REPO": repo, "BRANCH": "main", "BOT_LOGIN": botLogin, "ENABLED": "on",
		"EVENT": "push", "RETRY_SLEEP": "0", "RETRY_MAX": "3"}
	for k, v := range env {
		e[k] = v
	}
	out := filepath.Join(f.t.TempDir(), "out")
	r := f.run(script(f.t, "plan", "plan"), e, out)
	b, _ := os.ReadFile(out)
	for _, l := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(l, "=")
		switch k {
		case "live":
			r.live = v
		case "count":
			r.cnt = v
		case "plan":
			raw, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				f.t.Fatal(err)
			}
			r.raw = raw
			for _, row := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
				if row != "" {
					r.rows = append(r.rows, strings.Split(row, "\t"))
				}
			}
		}
	}
	return r
}

func (f *fx) tag(plan []byte, env map[string]string) result {
	f.t.Helper()
	e := map[string]string{"REPO": repo, "BRANCH": "main", "BOT_LOGIN": botLogin, "LIVE": "true",
		"APP_SLUG": "release-app", "TAGGER_NAME": "Release Bot", "TAGGER_EMAIL": "release@example.com",
		"PLAN_B64": base64.StdEncoding.EncodeToString(plan)}
	for k, v := range env {
		e[k] = v
	}
	return f.run(script(f.t, "tag", "tag"), e, "")
}

func (f *fx) releases() string {
	b, _ := os.ReadFile(filepath.Join(f.state, "release.log"))
	return string(b)
}

// want checks the plan rows, each as "kind tag pr bump".
func want(t *testing.T, r result, rows ...string) {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	var got []string
	for _, row := range r.rows {
		if len(row) != 6 {
			t.Fatalf("bad row %q", row)
		}
		got = append(got, strings.Join([]string{row[0], row[2], row[3], row[4]}, " "))
	}
	if strings.Join(got, "|") != strings.Join(rows, "|") {
		t.Fatalf("rows = %q, want %q\n%s", got, rows, r.out)
	}
}

func TestPlanCommitKinds(t *testing.T) {
	t.Run("squash", func(t *testing.T) {
		f := newFx(t, "v0.1.0", "example.com/ard")
		s := f.commit("t (#5)", human)
		f.pr(s, 5, "t")
		want(t, f.plan(nil), "tag v0.1.1 5 patch")
	})
	t.Run("merge commit", func(t *testing.T) {
		f := newFx(t, "v0.1.0", "example.com/ard")
		s := f.merge("Merge pull request #6 from a/b")
		f.pr(s, 6, "b")
		want(t, f.plan(nil), "tag v0.1.1 6 patch")
	})
	t.Run("direct push", func(t *testing.T) {
		f := newFx(t, "v0.1.0", "example.com/ard")
		s := f.commit("fix it", human)
		f.noPRs(s)
		r := f.plan(nil)
		want(t, r, "tag v0.1.1  patch")
		if !strings.Contains(r.out, "no merged PR") {
			t.Errorf("no warning:\n%s", r.out)
		}
	})
}

func TestPlanLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		want   string
		warn   string
	}{
		{"none", nil, "tag v0.1.1 5 patch", ""},
		{"minor", []string{"semver:minor"}, "tag v0.2.0 5 minor", ""},
		{"major", []string{"semver:major"}, "tag v1.0.0 5 major", ""},
		{"both", []string{"semver:minor", "semver:major"}, "tag v1.0.0 5 major", "both semver labels"},
		{"unrelated", []string{"bug"}, "tag v0.1.1 5 patch", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFx(t, "v0.1.0", "example.com/ard")
			s := f.commit("t (#5)", human)
			f.pr(s, 5, "t", tt.labels...)
			r := f.plan(nil)
			want(t, r, tt.want)
			if tt.warn != "" && !strings.Contains(r.out, tt.warn) {
				t.Errorf("missing warning %q:\n%s", tt.warn, r.out)
			}
		})
	}
}

func TestPlanSkipsArdCommits(t *testing.T) {
	forms := map[string]ident{
		"name":         {botLogin, "x@example.com"},
		"email":        {"x", "ard-test[bot]@users.noreply.github.com"},
		"numeric id":   {"x", "12345+ard-test[bot]@users.noreply.github.com"},
		"mixed case":   {"ARD-Test[BOT]", "x@example.com"},
		"mixed email":  {"x", "99+ARD-TEST[bot]@Users.Noreply.GitHub.com"},
		"control char": {"x\x01y", "x@example.com"},
	}
	for name, id := range forms {
		t.Run(name, func(t *testing.T) {
			f := newFx(t, "v0.1.0", "example.com/ard")
			f.commit("ard(plan): #1 [skip ci]", id)
			r := f.plan(nil)
			want(t, r)
			if r.cnt != "0" || !strings.Contains(r.out, "skip ") {
				t.Errorf("count %q\n%s", r.cnt, r.out)
			}
		})
	}
	t.Run("human after bot numbers from base", func(t *testing.T) {
		f := newFx(t, "v0.1.0", "example.com/ard")
		f.commit("ard: x", ident{botLogin, "x@example.com"})
		s := f.commit("h (#7)", human)
		f.pr(s, 7, "h")
		want(t, f.plan(nil), "tag v0.1.1 7 patch")
	})
	t.Run("a different login is not skipped", func(t *testing.T) {
		f := newFx(t, "v0.1.0", "example.com/ard")
		s := f.commit("h (#7)", ident{"other[bot]", "1+other[bot]@users.noreply.github.com"})
		f.pr(s, 7, "h")
		want(t, f.plan(nil), "tag v0.1.1 7 patch")
	})
}

func TestPlanTwoMerges(t *testing.T) {
	for _, tt := range []struct {
		label string
		want  []string
	}{
		{"", []string{"tag v0.1.1 1 patch", "tag v0.1.2 2 patch"}},
		{"semver:minor", []string{"tag v0.2.0 1 minor", "tag v0.2.1 2 patch"}},
	} {
		t.Run("label "+tt.label, func(t *testing.T) {
			f := newFx(t, "v0.1.0", "example.com/ard")
			a := f.commit("a (#1)", human)
			b := f.commit("b (#2)", human)
			if tt.label != "" {
				f.pr(a, 1, "a", tt.label)
			} else {
				f.pr(a, 1, "a")
			}
			f.pr(b, 2, "b")
			want(t, f.plan(nil), tt.want...)
		})
	}
}

func TestRerunsAndResume(t *testing.T) {
	f := newFx(t, "v0.1.0", "example.com/ard")
	a := f.commit("a (#1)", human)
	f.pr(a, 1, "a")
	r := f.plan(nil)
	want(t, r, "tag v0.1.1 1 patch")
	if tr := f.tag(r.raw, nil); tr.code != 0 {
		t.Fatalf("tag exit %d\n%s", tr.code, tr.out)
	}
	if got := git(t, f.clone, nil, "cat-file", "-t", "v0.1.1"); got != "tag" {
		t.Errorf("tag type %q", got)
	}
	if got := git(t, f.origin, nil, "rev-parse", "v0.1.1^{commit}"); got != a {
		t.Errorf("origin tag at %s", got)
	}
	if got := git(t, f.clone, nil, "for-each-ref", "--format=%(taggername)", "refs/tags/v0.1.1"); got != "Release Bot" {
		t.Errorf("tagger %q", got)
	}
	if rl := f.releases(); !strings.Contains(rl, "create v0.1.1 --verify-tag") {
		t.Errorf("release log %q", rl)
	}
	// Replan once the release exists: nothing to do.
	f.ghJSON("repos/"+repo+"/releases/tags/v0.1.1", map[string]any{})
	r2 := f.plan(nil)
	want(t, r2)
	if r2.cnt != "0" {
		t.Errorf("count %q", r2.cnt)
	}
	// Rerunning the old plan moves nothing.
	again := f.tag(r.raw, nil)
	if again.code != 2 || !strings.Contains(again.out, "already has version tag") {
		t.Errorf("rerun: exit %d\n%s", again.code, again.out)
	}
	if got := git(t, f.origin, nil, "rev-parse", "v0.1.1^{commit}"); got != a {
		t.Errorf("tag moved to %s", got)
	}
}

func TestResumeAfterReleaseFailure(t *testing.T) {
	f := newFx(t, "v0.1.0", "example.com/ard")
	a := f.commit("a (#1)", human)
	b := f.commit("b (#2)", human)
	f.pr(a, 1, "a")
	f.pr(b, 2, "b")
	r := f.plan(nil)
	want(t, r, "tag v0.1.1 1 patch", "tag v0.1.2 2 patch")
	if err := os.WriteFile(filepath.Join(f.state, "release.fail"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tr := f.tag(r.raw, nil)
	if tr.code != 2 || !strings.Contains(tr.out, "release for v0.1.1 failed") {
		t.Fatalf("exit %d\n%s", tr.code, tr.out)
	}
	if err := os.Remove(filepath.Join(f.state, "release.fail")); err != nil {
		t.Fatal(err)
	}
	// v0.1.1 has no release (404): backfill it, then tag the second commit.
	r2 := f.plan(nil)
	want(t, r2, "backfill v0.1.1  ", "tag v0.1.2 2 patch")
	if tr := f.tag(r2.raw, nil); tr.code != 0 {
		t.Fatalf("exit %d\n%s", tr.code, tr.out)
	}
}

func TestPlanAssociationLag(t *testing.T) {
	f := newFx(t, "v0.1.0", "example.com/ard")
	s := f.commit("a (#3)", human)
	f.pr(s, 3, "a", "semver:minor")
	// The first call has no association yet; the retry sees the PR.
	f.ghFile("repos/"+repo+"/commits/"+s+"/pulls", ".first", "[]")
	// The pulls/N fallback answers 404, so only the retry can find the PR.
	if err := os.Remove(filepath.Join(f.state, "api", "repos_o_r_pulls_3.json")); err != nil {
		t.Fatal(err)
	}
	want(t, f.plan(nil), "tag v0.2.0 3 minor")
}

func TestPlanFallsBackToPullNumber(t *testing.T) {
	f := newFx(t, "v0.1.0", "example.com/ard")
	s := f.commit("a (#3)", human)
	f.pr(s, 3, "a", "semver:minor")
	f.noPRs(s)
	want(t, f.plan(nil), "tag v0.2.0 3 minor")
}

func TestModulePathGuard(t *testing.T) {
	t.Run("major to v2 needs /v2", func(t *testing.T) {
		f := newFx(t, "v1.0.0", "example.com/ard")
		s := f.commit("a (#1)", human)
		f.pr(s, 1, "a", "semver:major")
		r := f.plan(nil)
		if r.code != 2 || !strings.Contains(r.out, "needs a module path ending in /v2") {
			t.Fatalf("exit %d\n%s", r.code, r.out)
		}
		if len(r.rows) != 0 {
			t.Errorf("plan written: %q", r.rows)
		}
	})
	t.Run("major to v2 with /v2", func(t *testing.T) {
		f := newFx(t, "v1.0.0", "example.com/ard")
		f.setGoMod("example.com/ard/v2")
		s := f.commit("a (#1)", human)
		f.pr(s, 1, "a", "semver:major")
		want(t, f.plan(nil), "tag v2.0.0 1 major")
	})
	t.Run("/v2 module below v2", func(t *testing.T) {
		f := newFx(t, "v0.1.0", "example.com/ard")
		f.setGoMod("example.com/ard/v2")
		s := f.commit("a (#1)", human)
		f.pr(s, 1, "a")
		r := f.plan(nil)
		if r.code != 2 || !strings.Contains(r.out, "needs a module path without a /vN suffix") {
			t.Fatalf("exit %d\n%s", r.code, r.out)
		}
	})
}

func TestBackfillRow(t *testing.T) {
	f := newFx(t, "v0.1.0", "example.com/ard")
	if err := os.Remove(filepath.Join(f.state, "api", "repos_o_r_releases_tags_v0.1.0.json")); err != nil {
		t.Fatal(err)
	}
	r := f.plan(nil)
	want(t, r, "backfill v0.1.0  ")
	if tr := f.tag(r.raw, nil); tr.code != 0 {
		t.Fatalf("exit %d\n%s", tr.code, tr.out)
	}
	if rl := f.releases(); !strings.Contains(rl, "create v0.1.0 --verify-tag") {
		t.Errorf("release log %q", rl)
	}
	if got := git(t, f.clone, nil, "tag", "-l"); got != "v0.1.0" {
		t.Errorf("tags %q", got)
	}
}

func TestPlanModes(t *testing.T) {
	t.Run("no tag dry run", func(t *testing.T) {
		f := newFx(t, "", "example.com/ard")
		r := f.plan(map[string]string{"ENABLED": ""})
		want(t, r)
		if r.live != "false" {
			t.Errorf("live %q", r.live)
		}
	})
	t.Run("no tag live", func(t *testing.T) {
		f := newFx(t, "", "example.com/ard")
		r := f.plan(nil)
		if r.code != 2 || !strings.Contains(r.out, "no version tag") {
			t.Errorf("exit %d\n%s", r.code, r.out)
		}
	})
	for _, tt := range []struct {
		name string
		env  map[string]string
		live string
	}{
		{"unset", map[string]string{"ENABLED": ""}, "false"},
		{"dispatch dry", map[string]string{"EVENT": "workflow_dispatch", "INPUT_DRY_RUN": "true"}, "false"},
		{"dispatch live", map[string]string{"EVENT": "workflow_dispatch", "INPUT_DRY_RUN": "false"}, "true"},
		{"push", nil, "true"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFx(t, "v0.1.0", "example.com/ard")
			if r := f.plan(tt.env); r.code != 0 || r.live != tt.live {
				t.Errorf("exit %d live %q\n%s", r.code, r.live, r.out)
			}
		})
	}
	t.Run("cap", func(t *testing.T) {
		f := newFx(t, "v0.1.0", "example.com/ard")
		a := f.commit("a (#1)", human)
		b := f.commit("b (#2)", human)
		f.pr(a, 1, "a")
		f.pr(b, 2, "b")
		r := f.plan(map[string]string{"MAX_TAGS": "1"})
		if r.code != 2 || !strings.Contains(r.out, "over the cap of 1") {
			t.Errorf("exit %d\n%s", r.code, r.out)
		}
	})
	t.Run("api error", func(t *testing.T) {
		f := newFx(t, "v0.1.0", "example.com/ard")
		s := f.commit("a (#1)", human)
		f.ghFile("repos/"+repo+"/commits/"+s+"/pulls", ".err", "")
		r := f.plan(nil)
		if r.code != 2 || len(r.rows) != 0 || !strings.Contains(r.out, "failed") {
			t.Errorf("exit %d\n%s", r.code, r.out)
		}
	})
}

func TestTagRefusals(t *testing.T) {
	f := newFx(t, "v0.1.0", "example.com/ard")
	s := f.commit("a (#1)", human)
	f.pr(s, 1, "a")
	r := f.plan(nil)
	want(t, r, "tag v0.1.1 1 patch")
	good := string(r.raw)
	tests := []struct {
		name string
		plan string
		env  map[string]string
		msg  string
	}{
		{"not live", good, map[string]string{"LIVE": "false"}, "not a live run"},
		{"ard's own app", good, map[string]string{"APP_SLUG": "ARD-test"}, "must not be ard's App"},
		{"no tagger", good, map[string]string{"TAGGER_EMAIL": ""}, "RELEASE_BOT_NAME"},
		{"short row", "tag\tx\n", nil, "want 6 fields"},
		{"bad sha", "tag\tzz\tv0.1.1\t1\tpatch\tt\n", nil, "malformed"},
		{"bad bump", "tag\t" + sha40 + "\tv0.1.1\t1\thuge\tt\n", nil, "bad bump"},
		{"unknown kind", "nope\t\t\t\t\t\n", nil, "unknown kind"},
		{"not first parent", "tag\t" + sha40 + "\tv0.1.1\t1\tpatch\tt\n", nil, "not on first-parent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := f.tag([]byte(tt.plan), tt.env)
			if tr.code != 2 || !strings.Contains(tr.out, tt.msg) {
				t.Errorf("exit %d, want 2 with %q\n%s", tr.code, tt.msg, tr.out)
			}
		})
	}
	if got := git(t, f.clone, nil, "tag", "-l"); got != "v0.1.0" {
		t.Errorf("a refusal made tags: %q", got)
	}
	if f.releases() != "" {
		t.Errorf("a refusal made a release: %q", f.releases())
	}
}

func TestTitleIsNeutralised(t *testing.T) {
	f := newFx(t, "v0.1.0", "example.com/ard")
	s := f.commit("a (#5)", human)
	f.pr(s, 5, "a `b`\nc 100% @x", "bug")
	r := f.plan(nil)
	want(t, r, "tag v0.1.1 5 patch")
	if strings.ContainsAny(r.rows[0][5], "\n\r") {
		t.Fatalf("title row %q", r.rows[0][5])
	}
	if tr := f.tag(r.raw, nil); tr.code != 0 {
		t.Fatalf("exit %d\n%s", tr.code, tr.out)
	}
	msg := git(t, f.clone, nil, "for-each-ref", "--format=%(contents)", "refs/tags/v0.1.1")
	if !strings.Contains(msg, "`a 'b' c 100% @x` ([#5](https://github.com/o/r/pull/5))") {
		t.Errorf("tag message %q", msg)
	}
}

func TestWorkflowShape(t *testing.T) {
	script(t, "plan", "plan")
	script(t, "tag", "tag")
	jobs := as[map[string]any](t, loadYAML(t)["jobs"], "jobs")
	plan := as[map[string]any](t, jobs["plan"], "plan")
	perms := as[map[string]any](t, plan["permissions"], "permissions")
	if perms["contents"] != "read" || perms["pull-requests"] != "read" || len(perms) != 2 {
		t.Errorf("plan permissions = %v, want contents and pull-requests read only", perms)
	}
}
