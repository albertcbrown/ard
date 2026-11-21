// Package gittest builds throwaway git repositories for tests.
package gittest

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Run runs git in dir and fails the test on error. It returns stdout.
func Run(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// NewRemote returns a bare repository with one commit on main and a clone of
// it with origin pointing at the bare repo.
func NewRemote(t testing.TB) (bare, clone string) {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	bare = filepath.Join(root, "remote.git")
	clone = filepath.Join(root, "clone")
	Run(t, root, "init", "--bare", "--initial-branch=main", bare)
	Run(t, root, "init", "--initial-branch=main", seed)
	Run(t, seed, "commit", "--allow-empty", "-m", "init")
	Run(t, seed, "push", bare, "main")
	Run(t, root, "clone", bare, clone)
	return bare, clone
}
