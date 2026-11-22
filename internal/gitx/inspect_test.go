package gitx

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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

func TestDiffNumstatAndAddedLines(t *testing.T) {
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

	write(t, dir, "mod.txt", "1\nTWO\n3\n4\nFIVE\nSIX\n") // changes line 2, 5 and appends 6
	write(t, dir, "app.txt", "a\nb\nc\nd\n")              // appends lines 3-4
	write(t, dir, "del.txt", "x\n")                       // pure deletion
	write(t, dir, "sp ace.txt", "s\nt\n")
	write(t, dir, "bin.dat", "\x00\x01\x02\x03")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "new.txt", "untracked\n")

	ns, err := g.DiffNumstat(ctx, dir, base)
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
	} {
		if got[path] != want {
			t.Errorf("numstat %s = %+v, want %+v", path, got[path], want)
		}
	}
	if _, ok := got["new.txt"]; ok {
		t.Error("an untracked file must not be in numstat")
	}

	al, err := g.AddedLines(ctx, dir, base)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]LineRange{
		"mod.txt":    {{2, 2}, {5, 6}},
		"app.txt":    {{3, 4}},
		"sp ace.txt": {{2, 2}},
	}
	if !reflect.DeepEqual(al, want) {
		t.Errorf("added lines = %v, want %v", al, want)
	}
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
