package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLsRemote(t *testing.T) {
	ctx := context.Background()
	g := New("")
	bare, clone := gittest.NewRemote(t)
	gittest.Run(t, clone, "push", "origin", "HEAD:refs/heads/worker/eng-12-fix-it", "HEAD:refs/heads/worker/eng-12-other", "HEAD:refs/heads/worker/eng-13-x")
	head := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	got, err := g.LsRemote(ctx, clone, bare, "refs/heads/worker/eng-12-*", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"refs/heads/worker/eng-12-fix-it": head, "refs/heads/worker/eng-12-other": head}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("refs = %v, want %v", got, want)
	}
	none, err := g.LsRemote(ctx, clone, bare, "refs/heads/worker/nope-*", nil)
	if err != nil || len(none) != 0 {
		t.Errorf("none = %v, %v", none, err)
	}
	if _, err := g.LsRemote(ctx, clone, filepath.Join(t.TempDir(), "missing.git"), "refs/heads/*", nil); err == nil {
		t.Error("a missing remote must fail")
	}
}

func TestSnapshotNumstatAndAddedLines(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, dir := gittest.NewRemote(t)
	write(t, dir, "mod.txt", "1\n2\n3\n4\n5\n")
	write(t, dir, "app.txt", "a\nb\n")
	write(t, dir, "del.txt", "x\ny\n")
	write(t, dir, "gone.txt", "g\n")
	write(t, dir, "sp ace.txt", "s\n")
	write(t, dir, "bin.dat", "\x00\x01\x02")
	gittest.Run(t, dir, "add", ".")
	gittest.Run(t, dir, "commit", "-m", "base")
	base := strings.TrimSpace(gittest.Run(t, dir, "rev-parse", "HEAD"))

	write(t, dir, "mod.txt", "1\nTWO\n3\n4\nFIVE\nSIX\n")
	write(t, dir, "app.txt", "a\nb\nc\nd\n")
	write(t, dir, "del.txt", "x\n")
	write(t, dir, "sp ace.txt", "s\nt\n")
	write(t, dir, "bin.dat", "\x00\x01\x02\x03")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "new.txt", "u1\nu2\nu3\n")

	s, err := g.Snapshot(ctx, dir, dir, base)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ns, err := s.Numstat(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]NumStat{}
	for _, n := range ns {
		got[n.Path] = n
	}
	for path, want := range map[string]NumStat{
		"mod.txt":    {Path: "mod.txt", Added: 3, Deleted: 2},
		"app.txt":    {Path: "app.txt", Added: 2},
		"del.txt":    {Path: "del.txt", Deleted: 1},
		"gone.txt":   {Path: "gone.txt", Deleted: 1},
		"sp ace.txt": {Path: "sp ace.txt", Added: 1},
		"bin.dat":    {Path: "bin.dat", Binary: true},
		"new.txt":    {Path: "new.txt", Added: 3},
	} {
		if got[path] != want {
			t.Errorf("numstat %s = %+v, want %+v", path, got[path], want)
		}
	}
	al, err := s.AddedLines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]LineRange{
		"mod.txt":    {{2, 2}, {5, 6}},
		"app.txt":    {{3, 4}},
		"sp ace.txt": {{2, 2}},
		"new.txt":    {{1, 3}},
	}
	if !reflect.DeepEqual(al, want) {
		t.Errorf("added lines = %v, want %v", al, want)
	}
}

// A line the agent adds that reads like a diff file header must not move the
// attribution of later hunks (the secret below it would be filtered out).
func TestAddedLinesIgnoresHeaderLookalikes(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, dir := gittest.NewRemote(t)
	write(t, dir, "a.txt", "one\ntwo\n")
	write(t, dir, "z.txt", "z\n")
	gittest.Run(t, dir, "add", ".")
	gittest.Run(t, dir, "commit", "-m", "base")
	base := strings.TrimSpace(gittest.Run(t, dir, "rev-parse", "HEAD"))
	write(t, dir, "a.txt", "one\n++ b/zzz\ntwo\nSECRET LINE\n")
	s, err := g.Snapshot(ctx, dir, dir, base)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	al, err := s.AddedLines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]LineRange{"a.txt": {{2, 2}, {4, 4}}}
	if !reflect.DeepEqual(al, want) {
		t.Errorf("added lines = %v, want %v", al, want)
	}
	// And the parser itself, on a hand-made diff.
	out := "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -0,0 +1,2 @@\n+x\n++++ b/zzz\n@@ -5 +9 @@\n+y\n"
	got, err := parseAdded(out)
	if err != nil || !reflect.DeepEqual(got, map[string][]LineRange{"a": {{1, 2}, {9, 9}}}) {
		t.Errorf("parseAdded = %v, %v", got, err)
	}
}

// What the agent did to its own index or gitdir must not change what the
// guard sees or run anything.
func TestSnapshotDoesNotTrustTheWorktree(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, clone := gittest.NewRemote(t)
	write(t, clone, "keep.txt", "harmless\n")
	write(t, clone, "other.txt", "o\n")
	gittest.Run(t, clone, "add", ".")
	gittest.Run(t, clone, "commit", "-m", "base")
	base := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))

	t.Run("skip-worktree and assume-unchanged do not hide edits", func(t *testing.T) {
		wt := filepath.Join(t.TempDir(), "wt")
		gittest.Run(t, clone, "worktree", "add", "--detach", wt, base)
		write(t, wt, "keep.txt", "harmless\nmore\n")
		gittest.Run(t, wt, "add", "keep.txt")
		gittest.Run(t, wt, "update-index", "--skip-worktree", "keep.txt")
		write(t, wt, "keep.txt", "harmless\nSECRET\n")
		write(t, wt, "other.txt", "changed\n")
		gittest.Run(t, wt, "update-index", "--assume-unchanged", "other.txt")
		write(t, wt, "other.txt", "changed again\n")
		got, err := g.ChangedSince(ctx, clone, wt, base)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Path != "keep.txt" || got[1].Path != "other.txt" {
			t.Fatalf("changes = %v", got)
		}
		s, err := g.Snapshot(ctx, clone, wt, base)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		al, _ := s.AddedLines(ctx)
		if !reflect.DeepEqual(al["keep.txt"], []LineRange{{2, 2}}) {
			t.Errorf("added = %v", al)
		}
		ns, _ := s.Numstat(ctx, "")
		if len(ns) != 2 {
			t.Errorf("numstat = %v", ns)
		}
	})

	t.Run("a planted gitdir runs no code", func(t *testing.T) {
		wt := filepath.Join(t.TempDir(), "wt")
		gittest.Run(t, clone, "worktree", "add", "--detach", wt, base)
		marker := filepath.Join(t.TempDir(), "ran")
		script := filepath.Join(t.TempDir(), "hook.sh")
		if err := os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null 2>&1\ntouch "+marker+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		// The agent's own gitdir: a copy of the repository with every code hook.
		evil := filepath.Join(t.TempDir(), "evil.git")
		gittest.Run(t, filepath.Dir(evil), "clone", "--bare", clone, evil)
		cfg := "[core]\n\tfsmonitor = " + script + "\n\tsshCommand = " + script + "\n\thooksPath = " + filepath.Dir(script) + "\n" +
			"[filter \"x\"]\n\tclean = " + script + "\n\tsmudge = " + script + "\n\trequired = false\n" +
			"[diff \"x\"]\n\ttextconv = " + script + "\n\tcommand = " + script + "\n[diff]\n\texternal = " + script + "\n"
		f, err := os.OpenFile(filepath.Join(evil, "config"), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(cfg); err != nil {
			t.Fatal(err)
		}
		f.Close()
		if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+evil+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		write(t, wt, ".gitattributes", "* filter=x diff=x\n")
		write(t, wt, "keep.txt", "changed\n")
		write(t, wt, "new.txt", "new\n")
		got, err := g.ChangedSince(ctx, clone, wt, base)
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		for _, c := range got {
			paths = append(paths, c.Path)
		}
		if strings.Join(paths, ",") != ".gitattributes,keep.txt,new.txt" {
			t.Errorf("changes = %v", paths)
		}
		s, err := g.Snapshot(ctx, clone, wt, base)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err := s.Numstat(ctx, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddedLines(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := g.MergeBase(ctx, clone, base, base); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("the agent's gitdir ran code")
		}
	})

	t.Run("modes are reported", func(t *testing.T) {
		wt := filepath.Join(t.TempDir(), "wt")
		gittest.Run(t, clone, "worktree", "add", "--detach", wt, base)
		if err := os.Symlink("keep.txt", filepath.Join(wt, "link")); err != nil {
			t.Fatal(err)
		}
		write(t, wt, "run.sh", "x\n")
		if err := os.Chmod(filepath.Join(wt, "run.sh"), 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := g.ChangedSince(ctx, clone, wt, base)
		if err != nil {
			t.Fatal(err)
		}
		modes := map[string]string{}
		for _, c := range got {
			modes[c.Path] = c.Mode
		}
		if modes["link"] != "120000" || modes["run.sh"] != "100755" {
			t.Errorf("modes = %v", modes)
		}
	})

	t.Run("a bare controller repository works", func(t *testing.T) {
		bare := filepath.Join(t.TempDir(), "ctl.git")
		gittest.Run(t, filepath.Dir(bare), "clone", "--bare", clone, bare)
		wt := filepath.Join(t.TempDir(), "wt")
		gittest.Run(t, bare, "worktree", "add", "--detach", wt, base)
		write(t, wt, "keep.txt", "edited\n")
		got, err := g.ChangedSince(ctx, bare, wt, base)
		if err != nil || len(got) != 1 || got[0].Path != "keep.txt" {
			t.Errorf("changes = %v, %v", got, err)
		}
	})
}

func TestMergeBase(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, dir := gittest.NewRemote(t)
	base := strings.TrimSpace(gittest.Run(t, dir, "rev-parse", "HEAD"))
	gittest.Run(t, dir, "commit", "--allow-empty", "-m", "one")
	one := strings.TrimSpace(gittest.Run(t, dir, "rev-parse", "HEAD"))
	gittest.Run(t, dir, "checkout", "-q", "-b", "side", base)
	gittest.Run(t, dir, "commit", "--allow-empty", "-m", "two")
	two := strings.TrimSpace(gittest.Run(t, dir, "rev-parse", "HEAD"))
	got, err := g.MergeBase(ctx, dir, one, two)
	if err != nil || got != base {
		t.Errorf("merge-base = %q, %v; want %q", got, err, base)
	}
	if _, err := g.MergeBase(ctx, dir, one, "nope"); err == nil {
		t.Error("an unknown commit must fail")
	}
}

func TestHunkAdded(t *testing.T) {
	for h, want := range map[string]struct {
		r  LineRange
		ok bool
	}{
		"@@ -1,2 +3,4 @@ func x()": {LineRange{3, 6}, true},
		"@@ -1 +7 @@":              {LineRange{7, 7}, true},
		"@@ -4,2 +3,0 @@":          {LineRange{}, false},
	} {
		r, ok, err := hunkAdded(h)
		if err != nil || ok != want.ok || r != want.r {
			t.Errorf("%q = %v %v %v", h, r, ok, err)
		}
	}
	if _, _, err := hunkAdded("@@ nonsense"); err == nil {
		t.Error("bad header must fail")
	}
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// Hashing an agent's files must not grow the controller's clone.
func TestSnapshotWritesNoObjectsToTheClone(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, clone := gittest.NewRemote(t)
	base := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	wt := filepath.Join(t.TempDir(), "wt")
	gittest.Run(t, clone, "worktree", "add", "--detach", wt, base)
	write(t, wt, "new.txt", "brand new content "+strings.Repeat("z", 5000)+"\n")
	before := countFiles(t, filepath.Join(clone, ".git", "objects"))
	s, err := g.Snapshot(ctx, clone, wt, base)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := s.Changed(ctx)
	if err != nil || len(ch) != 1 {
		t.Fatalf("changes = %v, %v", ch, err)
	}
	tmp := s.tmp
	s.Close()
	if after := countFiles(t, filepath.Join(clone, ".git", "objects")); after != before {
		t.Errorf("clone objects %d -> %d", before, after)
	}
	if _, err := os.Stat(tmp); err == nil {
		t.Error("the private object directory was not removed")
	}
}

func TestSnapshotLimits(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, clone := gittest.NewRemote(t)
	write(t, clone, "tracked.txt", "t\n")
	write(t, clone, "big-tracked.bin", strings.Repeat("b", 4096))
	write(t, clone, ".gitignore", "ignored/\n")
	gittest.Run(t, clone, "add", ".")
	gittest.Run(t, clone, "commit", "-m", "base")
	base := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	opts := SnapshotOptions{MaxFileBytes: 1024, MaxCandidates: 3}
	mk := func(t *testing.T) string {
		wt := filepath.Join(t.TempDir(), "wt")
		gittest.Run(t, clone, "worktree", "add", "--detach", wt, base)
		return wt
	}
	snap := func(wt string) error {
		s, err := g.Snapshot(ctx, clone, wt, base, opts)
		if err == nil {
			s.Close()
		}
		return err
	}
	t.Run("within limits, with an unchanged big tracked file and an ignored big file", func(t *testing.T) {
		wt := mk(t)
		write(t, wt, "ignored/huge.bin", strings.Repeat("i", 1<<20))
		write(t, wt, "a.txt", "a\n")
		if err := snap(wt); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a big new file", func(t *testing.T) {
		wt := mk(t)
		write(t, wt, "big.bin", strings.Repeat("x", 2048))
		var le *LimitError
		if err := snap(wt); !errors.As(err, &le) || le.Path != "big.bin" {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a tracked file grown past the limit", func(t *testing.T) {
		wt := mk(t)
		write(t, wt, "tracked.txt", strings.Repeat("x", 2048))
		var le *LimitError
		if err := snap(wt); !errors.As(err, &le) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("too many new files", func(t *testing.T) {
		wt := mk(t)
		for i := 0; i < 4; i++ {
			write(t, wt, fmt.Sprintf("n%d.txt", i), "x\n")
		}
		var le *LimitError
		if err := snap(wt); !errors.As(err, &le) || le.Path != "" || !strings.Contains(le.Why, "more than 3") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestMergeBaseAllCrissCross(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, dir := gittest.NewRemote(t)
	root := strings.TrimSpace(gittest.Run(t, dir, "rev-parse", "HEAD"))
	gittest.Run(t, dir, "checkout", "-q", "-b", "a", root)
	gittest.Run(t, dir, "commit", "--allow-empty", "-m", "a1")
	gittest.Run(t, dir, "checkout", "-q", "-b", "b", root)
	gittest.Run(t, dir, "commit", "--allow-empty", "-m", "b1")
	gittest.Run(t, dir, "checkout", "-q", "-b", "a2", "a")
	gittest.Run(t, dir, "merge", "-q", "--no-edit", "b")
	gittest.Run(t, dir, "checkout", "-q", "-b", "b2", "b")
	gittest.Run(t, dir, "merge", "-q", "--no-edit", "a")
	got, err := g.MergeBaseAll(ctx, dir, "a2", "b2")
	if err != nil || len(got) != 2 {
		t.Fatalf("merge-base --all = %v, %v; want two bases", got, err)
	}
	one, err := g.MergeBaseAll(ctx, dir, "a", "b")
	if err != nil || len(one) != 1 || one[0] != root {
		t.Errorf("simple case = %v, %v", one, err)
	}
}

func TestChangedPathsRenames(t *testing.T) {
	ctx := context.Background()
	_, clone := gittest.NewRemote(t)
	write(t, clone, "old/a.txt", "some content that is long enough to be detected as a rename\n")
	write(t, clone, "gone.txt", "bye\n")
	gittest.Run(t, clone, "add", ".")
	gittest.Run(t, clone, "commit", "-m", "add")
	from := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	if err := os.MkdirAll(filepath.Join(clone, "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "mv", "old/a.txt", "new/a.txt")
	gittest.Run(t, clone, "rm", "gone.txt")
	gittest.Run(t, clone, "commit", "-m", "move")
	to := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	got, err := New("").ChangedPaths(ctx, clone, from, to)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"gone.txt", "new/a.txt", "old/a.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}
}

func TestPatchIDAndParents(t *testing.T) {
	_, dir := gittest.NewRemote(t)
	g := New("")
	ctx := context.Background()
	run := func(args ...string) string { return strings.TrimSpace(gittest.Run(t, dir, args...)) }
	body := func(first, last string) []byte {
		return []byte(first + "\n" + strings.Repeat("x\n", 30) + last + "\n")
	}
	write := func(b []byte) {
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), b, 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", "f.txt")
	}
	write(body("a", "z"))
	run("commit", "-q", "-m", "one")
	c1 := run("rev-parse", "HEAD")
	write(body("a", "Z"))
	run("commit", "-q", "-m", "two")
	c2 := run("rev-parse", "HEAD")
	write(body("A", "Z"))
	run("commit", "-q", "-m", "three")
	c3 := run("rev-parse", "HEAD")
	// The same change to the last line at shifted offsets.
	write([]byte("new\nnew\n" + string(body("A", "Z"))))
	run("commit", "-q", "-m", "shift")
	c4 := run("rev-parse", "HEAD")
	write([]byte("new\nnew\n" + string(body("A", "Q"))))
	run("commit", "-q", "-m", "other")
	c5 := run("rev-parse", "HEAD")

	a, err := g.PatchID(ctx, dir, c1, c2, "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := g.PatchID(ctx, dir, c1, c2, "f.txt"); err != nil || a != b {
		t.Fatalf("same diff: %q %q %v", a, b, err)
	}
	if b, err := g.PatchID(ctx, dir, c3, c4, "f.txt"); err != nil || a == b {
		t.Fatalf("different diff must differ: %q %q %v", a, b, err)
	}
	if b, err := g.PatchID(ctx, dir, c4, c5, "f.txt"); err != nil || a == b {
		t.Fatalf("changed line must differ: %q %q %v", a, b, err)
	}
	if _, err := g.PatchID(ctx, dir, c1, c1, "f.txt"); err == nil {
		t.Error("empty diff: want error")
	}
	if _, err := g.PatchID(ctx, dir, c1, "nope", "f.txt"); err == nil {
		t.Error("bad rev: want error")
	}
	ps, err := g.Parents(ctx, dir, c2)
	if err != nil || len(ps) != 1 || ps[0] != c1 {
		t.Fatalf("parents = %v %v", ps, err)
	}
	if _, err := g.Parents(ctx, dir, "nope"); err == nil {
		t.Error("parents of bad rev: want error")
	}
}

func TestMergeTreeEqual(t *testing.T) {
	_, dir := gittest.NewRemote(t)
	g := New("")
	ctx := context.Background()
	run := func(args ...string) string { return strings.TrimSpace(gittest.Run(t, dir, args...)) }
	put := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", name)
	}
	lines := func(first, last string) string {
		return first + "\n" + strings.Repeat("x\n", 30) + last + "\n"
	}
	put("f.py", lines("a", "z"))
	run("commit", "-q", "-m", "base")
	base := run("rev-parse", "HEAD")
	run("checkout", "-q", "-b", "pr")
	put("f.py", lines("a", "PR"))
	run("commit", "-q", "-m", "pr")
	run("checkout", "-q", "main")
	put("f.py", lines("main", "z"))
	run("commit", "-q", "-m", "main")
	run("checkout", "-q", "pr")
	run("merge", "-q", "--no-edit", "main")
	clean := run("rev-parse", "HEAD")
	if ok, err := g.MergeTreeEqual(ctx, dir, clean); err != nil || !ok {
		t.Fatalf("clean merge: %v %v", ok, err)
	}

	// Same merge with one line indented: not mechanical.
	put("f.py", " "+lines("main", "PR"))
	tree := run("write-tree")
	ws := run("commit-tree", tree, "-p", "HEAD~1", "-p", "main", "-m", "ws")
	if ok, err := g.MergeTreeEqual(ctx, dir, ws); err != nil || ok {
		t.Fatalf("whitespace merge: %v %v", ok, err)
	}

	// A conflict resolved by hand: false, not an error.
	run("checkout", "-q", "-f", "-b", "c1", base)
	put("f.py", lines("c1", "z"))
	run("commit", "-q", "-m", "c1")
	run("checkout", "-q", "-b", "c2", base)
	put("f.py", lines("c2", "z"))
	run("commit", "-q", "-m", "c2")
	tree = run("write-tree")
	cm := run("commit-tree", tree, "-p", "c1", "-p", "c2", "-m", "resolved")
	if ok, err := g.MergeTreeEqual(ctx, dir, cm); err != nil || ok {
		t.Fatalf("conflict merge: %v %v", ok, err)
	}

	// Not a merge, and a bad rev: errors.
	if _, err := g.MergeTreeEqual(ctx, dir, base); err == nil {
		t.Error("non-merge: want error")
	}
	if _, err := g.MergeTreeEqual(ctx, dir, "nope"); err == nil {
		t.Error("bad rev: want error")
	}

	// A git that rejects merge-tree --write-tree fails closed.
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	stub := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = merge-tree ] && exit 129; done\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, err := New(stub).MergeTreeEqual(ctx, dir, clean); err == nil || ok {
		t.Fatalf("old git: %v %v, want error", ok, err)
	}
}
