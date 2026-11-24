package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
)

// Remove deletes the worktree and home of h and prunes git's registration.
func (w *Workspace) Remove(ctx context.Context, h executor.Handle) error {
	var errs []error
	foreign := w.cloneDir != "" && filepath.Clean(h.RepoDir) != w.cloneDir
	if _, err := os.Stat(h.Worktree); err == nil {
		ChmodWritable(h.Worktree)
		if foreign {
			// Stored before #113: RepoDir is the operator's checkout, where
			// ard never runs git. The registration there dangles until the
			// operator's own `git worktree prune`.
			w.log.Info("executor.cleanup_foreign_repo", "job", h.JobID, "repo_dir", h.RepoDir, "clone", w.cloneDir)
			if err := RemoveAllWritable(h.Worktree); err != nil {
				errs = append(errs, err)
			}
		} else if err := w.git.WorktreeRemove(context.WithoutCancel(ctx), h.RepoDir, h.Worktree, true); err != nil {
			errs = append(errs, err)
			RemoveAllWritable(h.Worktree)
		}
	}
	// Home holds a-w trees (sidecar, Go module cache); derived from Home, not h.SidecarDir
	if err := RemoveAllWritable(h.Home); err != nil {
		errs = append(errs, err)
	}
	if !foreign {
		if err := w.git.WorktreePrune(context.WithoutCancel(ctx), h.RepoDir); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Prune deletes host log files older than olderThan, then empty dirs.
func (w *Workspace) Prune(ctx context.Context, olderThan time.Duration) error {
	cutoff := w.now().Add(-olderThan)
	dir := filepath.Join(w.root, "logs")
	var dirs []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		if st, e := d.Info(); e == nil && st.ModTime().Before(cutoff) {
			return os.Remove(p)
		}
		return nil
	})
	for i := len(dirs) - 1; i > 0; i-- { // deepest first, keep logs/ itself
		os.Remove(dirs[i]) // fails harmlessly when not empty
	}
	if err != nil {
		return fmt.Errorf("prune logs: %w", err)
	}
	return nil
}
