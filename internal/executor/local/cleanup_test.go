package local

import (
	"os"
	"path/filepath"
	"testing"
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
	if err := removeAllWritable(home); err != nil {
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
	if err := removeAllWritable(home); err != nil {
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
	t.Cleanup(func() { chmodWritable(outside) })
	home := filepath.Join(base, "home")
	mustMkdir(t, home)
	if err := os.Symlink(outside, filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	if err := removeAllWritable(home); err != nil {
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
	if err := removeAllWritable(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Fatal(err)
	}
}
