package reaper

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

// retain is step 3: remove the worktree and host files of ended jobs 24 h
// after they ended, unless ard:keep is on the issue (then look again in 24 h).
// A failed job's host logs go too; a done job's logs stay until Prune. Branches
// are never deleted.
func (r *Reaper) retain(ctx context.Context) []error {
	now := r.now()
	jobs, err := r.d.Store.Expired(ctx, now, retentionBatch)
	if err != nil {
		return []error{fmt.Errorf("reaper: list expired jobs: %w", err)}
	}
	var errs []error
	for _, sj := range jobs {
		if err := r.retainOne(ctx, sj); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (r *Reaper) retainOne(ctx context.Context, sj state.Job) error {
	attrs := []any{"job", sj.ID, "issue", sj.Issue, "stage", sj.Stage, "role", sj.Role}
	target := sj.Issue
	if sj.Stage == closeout.StageReview {
		target = sj.PR
	}
	// Scanner jobs and dry-run (negative) numbers have no real issue to ask.
	if sj.Stage != "scanner" && target > 0 {
		is, err := r.d.Reader.GetIssue(ctx, target)
		switch {
		case errors.Is(err, github.ErrNotFound):
			// The issue is gone: nothing can hold the evidence.
		case err != nil:
			// Fail closed: keep the evidence and look again next tick.
			return fmt.Errorf("reap job %s: check %s on #%d: %w", sj.ID, labels.Keep, target, err)
		case hasLabel(is.Labels, labels.Keep):
			until := r.now().Add(keepFor)
			if err := r.d.Store.ExtendKeep(ctx, sj.ID, until); err != nil {
				return fmt.Errorf("reap job %s: extend keep: %w", sj.ID, err)
			}
			r.log().Info("reaper.keep_extended", append(attrs, "until", until)...)
			return nil
		}
	}
	h, ok := decodeHandle(sj.Handle)
	if ok {
		if err := r.d.Executor.Cleanup(ctx, h, executor.CleanupOptions{KeepWorktree: false}); err != nil {
			return fmt.Errorf("reap job %s: cleanup: %w", sj.ID, err)
		}
		if sj.Status == state.StatusFailed {
			for _, p := range []string{h.LogPath, h.StdoutPath} {
				if p == "" {
					continue
				}
				if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("reap job %s: remove %s: %w", sj.ID, p, err)
				}
			}
		}
	}
	if err := r.d.Store.MarkCleaned(ctx, sj.ID); err != nil {
		return fmt.Errorf("reap job %s: mark cleaned: %w", sj.ID, err)
	}
	r.log().Info("reaper.cleaned", attrs...)
	return nil
}

func hasLabel(ls []string, want string) bool {
	for _, l := range ls {
		if l == want {
			return true
		}
	}
	return false
}
