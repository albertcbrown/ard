//go:build unix

package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func roTree(t *testing.T, root string) {
	t.Helper()
	d := filepath.Join(root, "sub")
	mustMkdir(t, d)
	if err := os.WriteFile(filepath.Join(d, "f.go"), []byte("x"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d, 0o555); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveAllWritable_ReadOnlyTree(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	mod := filepath.Join(home, "go", "pkg", "mod", "x")
	mustMkdir(t, mod)
	roTree(t, mod)
	if err := os.Chmod(mod, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAllWritable(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatalf("home still exists: %v", err)
	}
}

func TestRemoveAllWritable_UnreadableDir(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	d := filepath.Join(home, "d")
	mustMkdir(t, d)
	if err := os.WriteFile(filepath.Join(d, "f"), []byte("x"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d, 0); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAllWritable(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatalf("home still exists: %v", err)
	}
}

func TestRemoveAllWritable_SymlinkNotFollowed(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	mustMkdir(t, outside)
	roTree(t, outside)
	if err := os.Chmod(outside, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ChmodWritable(outside) })
	home := filepath.Join(base, "home")
	mustMkdir(t, home)
	if err := os.Symlink(outside, filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAllWritable(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatalf("home still exists: %v", err)
	}
	for p, want := range map[string]os.FileMode{
		outside:                               0o555,
		filepath.Join(outside, "sub"):         0o555,
		filepath.Join(outside, "sub", "f.go"): 0o444,
	} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s mode %v, want %v", p, st.Mode().Perm(), want)
		}
	}
}

func TestRemoveAllWritable_Missing(t *testing.T) {
	if err := RemoveAllWritable(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Fatal(err)
	}
}

func TestLayout(t *testing.T) {
	l, _ := New(Options{Root: "/r", Git: nopGit{}})
	if got, err := l.WorktreePath("o/r", "J1"); err != nil || got != "/r/work/o/r/J1" {
		t.Error(got, err)
	}
	if got, err := l.HomePath("o/r", "J1"); err != nil || got != "/r/work/o/r/J1.home" {
		t.Error(got, err)
	}
	if got, err := l.LogDir("o/r", 0); err != nil || got != "/r/logs/o/r/noissue" {
		t.Error(got, err)
	}
	for name, f := range map[string]func(string) (string, error){
		"worktree": func(r string) (string, error) { return l.WorktreePath(r, "J1") },
		"home":     func(r string) (string, error) { return l.HomePath(r, "J1") },
		"logs":     func(r string) (string, error) { return l.LogDir(r, 7) },
	} {
		a, _ := f("a-b/c")
		b, _ := f("a/b-c")
		if a == b {
			t.Errorf("%s: a-b/c and a/b-c collide at %s", name, a)
		}
		x, _ := f("O/R")
		y, _ := f("o/r")
		if x != y {
			t.Errorf("%s: case differs: %s vs %s", name, x, y)
		}
		if _, err := f("../r"); err == nil {
			t.Errorf("%s: invalid repo accepted", name)
		}
	}
	ts := time.Date(2026, 11, 21, 10, 15, 0, 0, time.UTC)
	if got := LogBase("worker", "dev", ts); got != "worker-dev-20261121T101500.000Z" {
		t.Error(got)
	}
}

type nopGit struct{}

func (nopGit) RevParse(context.Context, string, string) (string, error)   { return "", nil }
func (nopGit) WorktreeAdd(context.Context, string, string, string) error  { return nil }
func (nopGit) WorktreeRemove(context.Context, string, string, bool) error { return nil }
func (nopGit) WorktreePrune(context.Context, string) error                { return nil }
