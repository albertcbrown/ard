package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

func TestCheckoutTree(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, clone := gittest.NewRemote(t)
	if err := os.WriteFile(filepath.Join(clone, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("run.sh", filepath.Join(clone, "link")); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(t.TempDir(), "ran")
	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "-m", "files")
	sha := gittest.Run(t, clone, "rev-parse", "HEAD")
	sha = sha[:40]

	gitDir := filepath.Join(t.TempDir(), "repo.git")
	if err := g.InitBare(ctx, gitDir); err != nil {
		t.Fatal(err)
	}
	if err := g.FetchRef(ctx, gitDir, clone, "+"+sha+":refs/ard/base", nil); err != nil {
		t.Fatal(err)
	}
	// A post-checkout hook must not run.
	hd := filepath.Join(gitDir, "hooks")
	os.MkdirAll(hd, 0o755)
	os.WriteFile(filepath.Join(hd, "post-checkout"), []byte("#!/bin/sh\ntouch "+hook+"\n"), 0o755)

	wt := filepath.Join(t.TempDir(), "wt")
	if err := g.CheckoutTree(ctx, gitDir, wt, sha); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(wt, "run.sh")); err != nil || st.Mode().Perm()&0o100 == 0 {
		t.Errorf("exec bit lost: %v %v", st, err)
	}
	if dst, err := os.Readlink(filepath.Join(wt, "link")); err != nil || dst != "run.sh" {
		t.Errorf("symlink = %q, %v", dst, err)
	}
	if _, err := os.Lstat(filepath.Join(wt, ".git")); err == nil {
		t.Error("work tree has a .git")
	}
	if _, err := os.Stat(hook); err == nil {
		t.Error("hook ran")
	}
}
