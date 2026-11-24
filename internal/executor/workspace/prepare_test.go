//go:build unix

package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
)

// recGit is a fake worktree runner: WorktreeAdd makes the directory.
type recGit struct {
	nopGit
	pruned, removed int
}

func (g *recGit) RevParse(context.Context, string, string) (string, error) { return "abc", nil }
func (g *recGit) WorktreeAdd(_ context.Context, _, dir, _ string) error {
	return os.MkdirAll(dir, 0o755)
}
func (g *recGit) WorktreeRemove(_ context.Context, _, dir string, _ bool) error {
	g.removed++
	os.RemoveAll(dir)
	return nil
}
func (g *recGit) WorktreePrune(context.Context, string) error { g.pruned++; return nil }

func testJob() executor.Job {
	return executor.Job{ID: "J1", Repo: "o/r", Issue: 7, Stage: "worker", Role: "dev",
		RepoDir: "/repo", Ref: "HEAD", RoleDir: "/role", Timeout: time.Minute}
}

func TestDiscard(t *testing.T) {
	g := &recGit{}
	w, err := New(Options{Root: t.TempDir(), Git: g})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h, err := w.Prepare(ctx, testJob())
	if err != nil {
		t.Fatal(err)
	}
	if h.Kind != "host" || h.View.ConfigDir != filepath.Join(h.Home, ConfigDirName) {
		t.Errorf("handle = %+v", h)
	}
	if err := w.Discard(ctx, h); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{h.Worktree, h.Home, h.LogPath, h.StdoutPath} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s remains: %v", p, err)
		}
	}
	if g.pruned != 1 || g.removed != 1 {
		t.Errorf("pruned %d removed %d, want 1 1", g.pruned, g.removed)
	}
}

// A failure after the home exists removes worktree and home.
func TestPrepareRollback(t *testing.T) {
	g := &recGit{}
	root := t.TempDir()
	w, _ := New(Options{Root: root, Git: g})
	j := testJob()
	// Block the log dir so the failure comes after home creation.
	logDir, _ := w.LogDir(j.Repo, j.Issue)
	os.MkdirAll(filepath.Dir(logDir), 0o755)
	os.WriteFile(logDir, []byte("file"), 0o644)
	if _, err := w.Prepare(context.Background(), j); err == nil {
		t.Fatal("want error")
	}
	wt, _ := w.WorktreePath(j.Repo, j.ID)
	home, _ := w.HomePath(j.Repo, j.ID)
	for _, p := range []string{wt, home} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s remains: %v", p, err)
		}
	}
	if g.pruned != 1 {
		t.Errorf("pruned %d", g.pruned)
	}
}

func TestRemoveForeignRepoMakesNoGitCall(t *testing.T) {
	g := &recGit{}
	w, _ := New(Options{Root: t.TempDir(), Git: g, CloneDir: "/clone"})
	ctx := context.Background()
	h, err := w.Prepare(ctx, testJob()) // RepoDir /repo != CloneDir
	if err != nil {
		t.Fatal(err)
	}
	g.pruned, g.removed = 0, 0
	if err := w.Remove(ctx, h); err != nil {
		t.Fatal(err)
	}
	if g.pruned != 0 || g.removed != 0 {
		t.Errorf("git called: %+v", g)
	}
	if _, err := os.Lstat(h.Worktree); !os.IsNotExist(err) {
		t.Error("worktree remains")
	}
}
