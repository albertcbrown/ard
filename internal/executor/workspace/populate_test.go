package workspace

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/albertcbrown/ard/internal/executor"
)

func TestScrubGuards(t *testing.T) {
	const rel = ".ard/7/worker-dev.md"
	tests := []struct {
		name  string
		setup func(t *testing.T, repo string)
		fail  bool
	}{
		{"parent is a symlink", func(t *testing.T, repo string) {
			if err := os.MkdirAll(filepath.Join(repo, "elsewhere"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, "elsewhere/worker-dev.md"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(repo, ".ard"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../elsewhere", filepath.Join(repo, ".ard/7")); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"target is a directory", func(t *testing.T, repo string) {
			if err := os.MkdirAll(filepath.Join(repo, rel), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, rel, "keep"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"target is a symlink: link removed", func(t *testing.T, repo string) {
			if err := os.MkdirAll(filepath.Join(repo, ".ard/7"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../../a.txt", filepath.Join(repo, rel)); err != nil {
				t.Fatal(err)
			}
		}, false},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			wt := t.TempDir()
			c.setup(t, wt)
			err := scrub(wt, []string{rel})
			if (err != nil) != c.fail {
				t.Fatalf("err = %v, want failure %v", err, c.fail)
			}
			if !c.fail {
				if _, err := os.Lstat(filepath.Join(wt, rel)); err == nil {
					t.Error("link still there")
				}
			}
			if c.name == "parent is a symlink" {
				if _, err := os.Stat(filepath.Join(wt, "elsewhere/worker-dev.md")); err != nil {
					t.Error("file behind the symlink was removed")
				}
			}
		})
	}
}

func TestPopulate(t *testing.T) {
	t.Run("inputs and home", func(t *testing.T) {
		for _, mask := range []int{0o077, 0o022} {
			old := syscall.Umask(mask)
			tree, home := t.TempDir(), filepath.Join(t.TempDir(), "h")
			p, err := Populate(PopulateSpec{Tree: tree, Home: home, Scrub: []string{"gone"},
				Inputs: []executor.File{{Path: "a/b/in.txt", Data: []byte("x")}}})
			syscall.Umask(old)
			if err != nil {
				t.Fatal(err)
			}
			if b, err := os.ReadFile(filepath.Join(tree, "a/b/in.txt")); err != nil || string(b) != "x" {
				t.Errorf("input: %q %v", b, err)
			}
			if p.ConfigDir != filepath.Join(home, ConfigDirName) || len(p.Scrubbed) != 1 || p.Scrubbed[0] != "gone" {
				t.Errorf("populated = %+v", p)
			}
			for _, d := range []string{filepath.Join(home, "tmp"), p.ConfigDir} {
				if st, err := os.Stat(d); err != nil || st.Mode().Perm() != 0o700 {
					t.Errorf("%s: %v %v", d, st, err)
				}
			}
		}
	})
	t.Run("no home", func(t *testing.T) {
		p, err := Populate(PopulateSpec{Tree: t.TempDir()})
		if err != nil || p.ConfigDir != "" {
			t.Errorf("%+v %v", p, err)
		}
	})
	t.Run("input over symlink fails", func(t *testing.T) {
		tree := t.TempDir()
		if err := os.Symlink("elsewhere", filepath.Join(tree, "in.txt")); err != nil {
			t.Fatal(err)
		}
		_, err := Populate(PopulateSpec{Tree: tree, Inputs: []executor.File{{Path: "in.txt", Data: []byte("x")}}})
		if err == nil {
			t.Error("want error")
		}
		if _, err := os.Lstat(filepath.Join(tree, "elsewhere")); err == nil {
			t.Error("write followed the symlink")
		}
	})
	t.Run("scrub of a directory fails", func(t *testing.T) {
		tree := t.TempDir()
		os.Mkdir(filepath.Join(tree, "d"), 0o755)
		if _, err := Populate(PopulateSpec{Tree: tree, Scrub: []string{"d"}}); err == nil {
			t.Error("want error")
		}
	})
}
