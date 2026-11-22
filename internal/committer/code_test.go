package committer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

func TestCodeMessage(t *testing.T) {
	long := strings.Repeat("x", 200)
	tests := []struct {
		name, typ, title, body, job string
		check                       func(t *testing.T, msg string)
		invalid                     bool
	}{
		{name: "basic", typ: "feat", title: "Add thing", body: "Does it.", job: "j1", check: func(t *testing.T, m string) {
			if m != "feat: Add thing (#12)\n\nDoes it.\n\nArd-Job: j1" {
				t.Errorf("got %q", m)
			}
		}},
		{name: "long title", typ: "fix", title: long, job: "j1", check: func(t *testing.T, m string) {
			first, _, _ := strings.Cut(m, "\n")
			if !strings.HasSuffix(first, "… (#12)") || utf8.RuneCountInString(first) > 72 {
				t.Errorf("first line %q (%d)", first, utf8.RuneCountInString(first))
			}
		}},
		{name: "whitespace", typ: "fix", title: "a\nb\tc\r\nd", job: "j1", check: func(t *testing.T, m string) {
			if !strings.HasPrefix(m, "fix: a b c d (#12)\n") {
				t.Errorf("got %q", m)
			}
		}},
		{name: "skip tokens", typ: "fix", title: "[skip ci] [CI SKIP] skip-checks: true", body: "x [skip ci]\nskip-checks: true\n[no ci]", job: "j1", check: func(t *testing.T, m string) {
			l := strings.ToLower(m)
			if strings.Contains(l, "[skip ci]") || strings.Contains(l, "[ci skip]") || strings.Contains(l, "[no ci]") || strings.Contains(l, "skip-checks:") {
				t.Errorf("not defused: %q", m)
			}
		}},
		{name: "refs", typ: "fix", title: "Fixes #98", body: "Fixes #99\nFixes &#35;99\nFixes \\#99", job: "j1", check: func(t *testing.T, m string) {
			if strings.Contains(m, "#99") || strings.Contains(m, "#98") || strings.Contains(m, "&#35;") || strings.Contains(m, `\#`) {
				t.Errorf("closing ref remains: %q", m)
			}
		}},
		{name: "forged trailer", typ: "fix", title: "t", body: "text\n\nArd-Job: FAKE", job: "real", check: func(t *testing.T, m string) {
			if !strings.HasSuffix(m, "\n\nArd-Job: real") {
				t.Errorf("got %q", m)
			}
		}},
		{name: "cf dropped", typ: "fix", title: "a​b", body: "c‮d\x07e", job: "j1", check: func(t *testing.T, m string) {
			if m != "fix: a b (#12)\n\ncde\n\nArd-Job: j1" {
				t.Errorf("got %q", m)
			}
		}},
		{name: "empty body", typ: "fix", title: "t", job: "j1", check: func(t *testing.T, m string) {
			if m != "fix: t (#12)\n\nArd-Job: j1" {
				t.Errorf("got %q", m)
			}
		}},
		{name: "blank run and long body", typ: "fix", title: "t", body: "a\n\n\n\n\nb\n" + strings.Repeat("line\n", 2000), job: "j1", check: func(t *testing.T, m string) {
			if strings.Contains(m, "\n\n\n") || len([]rune(m)) > 4100 {
				t.Errorf("len %d", len(m))
			}
		}},
		{name: "empty title", typ: "fix", title: " \n ", job: "j1", invalid: true},
		{name: "bad type", typ: "Fix!", title: "t", job: "j1", invalid: true},
		{name: "no job", typ: "fix", title: "t", invalid: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := CodeMessage(tc.typ, tc.title, 12, tc.body, tc.job)
			if tc.invalid {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(strings.ToLower(m), "[skip ci]") {
				t.Errorf("skip ci in %q", m)
			}
			tc.check(t, m)
		})
	}
}

type codeEnv struct {
	*env
	b    *Branches
	base string
	src  string
}

// newCodeEnv seeds main with a.txt, run.sh (100755) and old.txt.
func newCodeEnv(t *testing.T, fx Effects) *codeEnv {
	t.Helper()
	e := newEnv(t)
	for name, body := range map[string]string{"a.txt": "a\n", "run.sh": "#!/bin/sh\n", "old.txt": "old\n"} {
		if err := os.WriteFile(filepath.Join(e.clone, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(e.clone, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, e.clone, "add", "-A")
	gittest.Run(t, e.clone, "commit", "-q", "-m", "seed")
	gittest.Run(t, e.clone, "push", "-q", "origin", "main")
	if fx == nil {
		fx = e.live()
	}
	b := NewBranches(e.cfg, fx, e.g)
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	return &codeEnv{env: e, b: b, base: e.head(t, "main"), src: realTemp(t)}
}

func (c *codeEnv) srcFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(c.src, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (c *codeEnv) req(files ...CodeFile) CodeRequest {
	return CodeRequest{Issue: 12, Role: "engineer", JobID: "job1", Type: "feat", Title: "Add thing",
		Body: "Summary.", Base: c.base, Files: files}
}

const brName = "worker/engineer-12-x"

func (c *codeEnv) tree(t *testing.T, ref string) string {
	return gittest.Run(t, c.bare, "ls-tree", "-r", ref)
}

func TestCommitCodeCreateAndUpdate(t *testing.T) {
	c := newCodeEnv(t, nil)
	ctx := context.Background()
	r := c.req(
		CodeFile{Path: "new.go", Src: c.srcFile(t, "new.go", "package x\n")},
		CodeFile{Path: "old.txt", Delete: true},
		CodeFile{Path: "run2.sh", Src: c.srcFile(t, "run2.sh", "#!/bin/sh\n"), Exec: true},
	)
	r.Create = true
	res, err := c.b.CommitCode(ctx, brName, r)
	if err != nil {
		t.Fatal(err)
	}
	head := c.head(t, brName)
	if res.SHA != head || res.Attempts != 1 {
		t.Fatalf("res %+v head %s", res, head)
	}
	if n := strings.TrimSpace(gittest.Run(t, c.bare, "rev-list", "--count", c.base+".."+brName)); n != "1" {
		t.Fatalf("ahead by %s", n)
	}
	if p := strings.TrimSpace(gittest.Run(t, c.bare, "rev-parse", brName+"^")); p != c.base {
		t.Fatalf("parent %s", p)
	}
	tree := c.tree(t, brName)
	for _, want := range []string{"100644 blob", "100755 blob"} {
		if !strings.Contains(tree, want) {
			t.Errorf("no %q in tree", want)
		}
	}
	if strings.Contains(tree, "old.txt") || !strings.Contains(tree, "\tnew.go") {
		t.Errorf("tree:\n%s", tree)
	}
	if !strings.Contains(tree, "100755 blob") || !strings.Contains(tree, "\trun2.sh") {
		t.Errorf("tree:\n%s", tree)
	}
	for _, l := range strings.Split(tree, "\n") {
		if strings.HasSuffix(l, "\trun2.sh") && !strings.HasPrefix(l, "100755") {
			t.Errorf("run2.sh mode: %s", l)
		}
	}
	msg := gittest.Run(t, c.bare, "log", "-1", "--format=%an <%ae>|%cn <%ce>|%B", brName)
	if !strings.Contains(msg, "ard[bot] <4242+ard[bot]@users.noreply.github.com>|ard[bot] <4242+ard[bot]@users.noreply.github.com>|feat: Add thing (#12)\n\nSummary.\n\nArd-Job: job1") {
		t.Errorf("message: %q", msg)
	}
	if strings.Contains(msg, "[skip ci]") {
		t.Error("skip ci")
	}

	// Create again: the branch exists.
	before := c.head(t, brName)
	// (A byte-identical commit would push as "up to date"; vary the title.)
	r2 := r
	r2.Title = "Another thing"
	if _, err := c.b.CommitCode(ctx, brName, r2); !errors.Is(err, ErrBranchExists) {
		t.Fatalf("create again: %v", err)
	}
	if c.head(t, brName) != before {
		t.Fatal("remote moved")
	}

	// Fast-forward: keep an exec bit, drop another.
	u := c.req(
		CodeFile{Path: "run.sh", Src: c.srcFile(t, "run.sh", "#!/bin/sh\necho\n")},
		CodeFile{Path: "run2.sh", Src: c.srcFile(t, "run2.sh2", "#!/bin/sh\necho 2\n"), Exec: true},
	)
	u.Base = before
	if _, err := c.b.CommitCode(ctx, brName, u); err != nil {
		t.Fatal(err)
	}
	if p := strings.TrimSpace(gittest.Run(t, c.bare, "rev-parse", brName+"^")); p != before {
		t.Fatalf("not a fast-forward: parent %s", p)
	}
	for _, l := range strings.Split(c.tree(t, brName), "\n") {
		switch {
		case strings.HasSuffix(l, "\trun.sh") && !strings.HasPrefix(l, "100644"):
			t.Errorf("run.sh should lose exec: %s", l)
		case strings.HasSuffix(l, "\trun2.sh") && !strings.HasPrefix(l, "100755"):
			t.Errorf("run2.sh should keep exec: %s", l)
		}
	}
}

func TestCommitCodeMovedBranch(t *testing.T) {
	c := newCodeEnv(t, nil)
	ctx := context.Background()
	r := c.req(CodeFile{Path: "new.go", Src: c.srcFile(t, "new.go", "package x\n")})
	r.Create = true
	if _, err := c.b.CommitCode(ctx, brName, r); err != nil {
		t.Fatal(err)
	}
	base := c.head(t, brName)
	// A .ard commit lands on the branch after base.
	if _, err := c.b.Commit(ctx, brName, req(12, "review-x.md", "ok")); err != nil {
		t.Fatal(err)
	}
	moved := c.head(t, brName)

	// Another path: lands on top.
	u := c.req(CodeFile{Path: "other.go", Src: c.srcFile(t, "other.go", "package y\n")})
	u.Base = base
	if _, err := c.b.CommitCode(ctx, brName, u); err != nil {
		t.Fatal(err)
	}
	if p := strings.TrimSpace(gittest.Run(t, c.bare, "rev-parse", brName+"^")); p != moved {
		t.Fatalf("parent %s, want %s", p, moved)
	}

	// A declared path changed since Base: conflict, nothing pushed.
	if _, err := c.b.Commit(ctx, brName, Request{Issue: 12, Stage: "review", Role: "x", Summary: "s",
		Files: []File{{Path: ".ard/12/y.md", Data: []byte("y")}}}); err != nil {
		t.Fatal(err)
	}
	before := c.head(t, brName)
	k := c.req(CodeFile{Path: "other.go", Src: c.srcFile(t, "other2.go", "package z\n")})
	k.Base = base
	_, err := c.b.CommitCode(ctx, brName, k)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "other.go") {
		t.Fatalf("err = %v", err)
	}
	if c.head(t, brName) != before {
		t.Fatal("remote moved")
	}
}

func TestCommitCodeRejections(t *testing.T) {
	c := newCodeEnv(t, nil)
	ctx := context.Background()
	gittest.Run(t, c.clone, "push", "-q", "origin", "main:refs/heads/"+brName)
	before := c.head(t, brName)
	ok := c.srcFile(t, "ok.go", "x")

	link := filepath.Join(c.src, "link")
	if err := os.Symlink(ok, link); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(c.src, "big")
	if err := os.WriteFile(big, make([]byte, MaxFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	r := c.req(
		CodeFile{Path: ".github/x", Src: ok},
		CodeFile{Path: ".ard/config.yaml", Src: ok},
		CodeFile{Path: "l.go", Src: link},
		CodeFile{Path: ".ard-input/12/issue.md", Src: ok},
		CodeFile{Path: "a/.git/x", Src: ok},
		CodeFile{Path: "k.pem", Src: ok},
		CodeFile{Path: "b.bin", Src: big},
	)
	_, err := c.b.CommitCode(ctx, brName, r)
	var ge *GuardError
	if !errors.As(err, &ge) || len(ge.Paths) != 7 || !errors.Is(err, ErrGuard) {
		t.Fatalf("err = %v", err)
	}
	if c.head(t, brName) != before {
		t.Fatal("remote moved")
	}

	good := CodeFile{Path: "g.go", Src: ok}
	shapes := map[string]CodeRequest{
		"exec delete": c.req(CodeFile{Path: "a.txt", Delete: true, Exec: true}),
		"duplicate":   c.req(good, good),
		"none":        c.req(),
		"both":        c.req(CodeFile{Path: "a", Src: ok, Delete: true}),
		"neither":     c.req(CodeFile{Path: "a"}),
	}
	many := make([]CodeFile, MaxFiles+1)
	for i := range many {
		many[i] = CodeFile{Path: "f" + strings.Repeat("a", i%10) + string(rune('a'+i%26)) + string(rune('a'+i/26)), Src: ok}
	}
	shapes["257 files"] = c.req(many...)
	badBase := c.req(good)
	badBase.Base = "main"
	shapes["bad base"] = badBase
	noJob := c.req(good)
	noJob.JobID = ""
	shapes["no job"] = noJob
	for name, rq := range shapes {
		if _, err := c.b.CommitCode(ctx, brName, rq); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := c.b.CommitCode(ctx, "main", c.req(good)); !errors.Is(err, ErrInvalid) {
		t.Errorf("main: %v", err)
	}
	if _, err := c.b.CommitCode(ctx, "worker/missing", c.req(good)); !errors.Is(err, ErrBranchGone) {
		t.Errorf("missing: %v", err)
	}
	// Create with no effective change.
	nc := c.req(CodeFile{Path: "a.txt", Src: c.srcFile(t, "a.txt", "a\n")})
	nc.Create = true
	if _, err := c.b.CommitCode(ctx, "worker/engineer-12-nochange", nc); !errors.Is(err, ErrInvalid) {
		t.Errorf("no change: %v", err)
	}
	if out := gittest.Run(t, c.bare, "branch", "--list", "worker/engineer-12-nochange"); strings.TrimSpace(out) != "" {
		t.Errorf("branch created: %s", out)
	}
}

func TestCommitCodeDryRun(t *testing.T) {
	e := newEnv(t)
	ledger := filepath.Join(t.TempDir(), "ledger.jsonl")
	d, err := effects.NewDryRun(effects.DryRunOptions{Log: e.cfg.Log, Staged: e.g.StagedPaths, LedgerPath: ledger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	e.cfg.Bot = bot
	b := NewBranches(e.cfg, d, e.g)
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	c := &codeEnv{env: e, b: b, base: e.head(t, "main"), src: realTemp(t)}
	r := c.req(CodeFile{Path: "new.go", Src: c.srcFile(t, "new.go", "package x\n")})
	r.Create = true
	res, err := b.CommitCode(context.Background(), brName, r)
	if err != nil || !res.Synthetic {
		t.Fatalf("%+v, %v", res, err)
	}
	if out := gittest.Run(t, e.bare, "branch", "--list", brName); strings.TrimSpace(out) != "" {
		t.Fatalf("remote has branch: %s", out)
	}
	led, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	s := string(led)
	if !strings.Contains(s, `"op":"Commit"`) || !strings.Contains(s, `"op":"Push"`) || !strings.Contains(s, strings.Repeat("0", 40)) {
		t.Errorf("ledger:\n%s", s)
	}
	// Synthetic issue: validated, nothing touches git.
	r.Issue = -12
	if res, err := b.CommitCode(context.Background(), brName, r); err != nil || !res.Synthetic {
		t.Fatalf("%+v, %v", res, err)
	}
}

func TestCommitCodeSerialisedWithCommit(t *testing.T) {
	c := newCodeEnv(t, nil)
	ctx := context.Background()
	r := c.req(CodeFile{Path: "new.go", Src: c.srcFile(t, "new.go", "package x\n")})
	r.Create = true
	if _, err := c.b.CommitCode(ctx, brName, r); err != nil {
		t.Fatal(err)
	}
	base := c.head(t, brName)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := c.b.Commit(ctx, brName, req(12, "x.md", "x")); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		u := c.req(CodeFile{Path: "two.go", Src: c.srcFile(t, "two.go", "package y\n")})
		u.Base = base
		if _, err := c.b.CommitCode(ctx, brName, u); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	if n := strings.TrimSpace(gittest.Run(t, c.bare, "rev-list", "--count", base+".."+brName)); n != "2" {
		t.Fatalf("ahead by %s", n)
	}
	tree := c.tree(t, brName)
	if !strings.Contains(tree, "\ttwo.go") || !strings.Contains(tree, "\t.ard/12/x.md") {
		t.Fatalf("tree:\n%s", tree)
	}
}

func TestArdLaneStagesNonExec(t *testing.T) {
	c := newCodeEnv(t, nil)
	ctx := context.Background()
	// An .ard file that is 100755 on the branch is committed back as 100644.
	gittest.Run(t, c.clone, "checkout", "-q", "-b", "tmp")
	p := filepath.Join(c.clone, ".ard", "12")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "x.md"), []byte("1"), 0o755); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, c.clone, "add", "-A")
	gittest.Run(t, c.clone, "commit", "-q", "-m", "exec")
	gittest.Run(t, c.clone, "push", "-q", "origin", "tmp:refs/heads/"+brName)
	if _, err := c.b.Commit(ctx, brName, req(12, "x.md", "2")); err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(c.tree(t, brName), "\n") {
		if strings.HasSuffix(l, "\t.ard/12/x.md") && !strings.HasPrefix(l, "100644") {
			t.Errorf("mode: %s", l)
		}
	}
}

// realTemp is a temp dir with symlinks (macOS /var) resolved, which readSrc requires.
func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A path that escapes the worktree must be refused by the lane itself, even
// when the path guards are bypassed.
func TestLaneRefusesEscape(t *testing.T) {
	c := newCodeEnv(t, nil)
	gittest.Run(t, c.clone, "push", "-q", "origin", "main:refs/heads/"+brName)
	l, err := c.b.laneFor(brName)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(c.work, "victim.txt")
	keep := filepath.Join(c.work, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Worktree is <work>/committer-branch-...; "../victim.txt" lands in c.work.
	for _, f := range []snapFile{
		{path: "../victim.txt", data: []byte("x")},
		{path: "../keep.txt", del: true},
	} {
		s := snap{files: []snapFile{f}, declared: []string{f.path}, msg: "m"}
		_, err := l.run(context.Background(), s)
		if !errors.Is(err, ErrGuard) {
			t.Errorf("%s: %v", f.path, err)
		}
	}
	if _, err := os.Stat(outside); err == nil {
		t.Error("file written outside the worktree")
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "keep" {
		t.Error("host file removed")
	}
	// And through the public API.
	for _, p := range []string{"../../victim.txt", ".."} {
		r := c.req(CodeFile{Path: p, Src: c.srcFile(t, "ok.go", "x")})
		r.Create = true
		if _, err := c.b.CommitCode(context.Background(), "worker/engineer-12-esc", r); !errors.Is(err, ErrGuard) {
			t.Errorf("%s: %v", p, err)
		}
	}
}
