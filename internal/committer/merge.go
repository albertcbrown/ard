package committer

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/albertcbrown/ard/internal/effects"
)

// mergeRef is the private ref the lane fetches main into for a merge. It is
// not upstreamRef("main"): nothing else writes it.
const mergeRef = "refs/ard/merge/main"

// runMerge commits s as a two-parent merge of the branch tip and the tip of
// main. It does not run `git merge`: the tree is main's tree (s.mergeParent)
// with the snapshot's files written over it, so outside the declared paths the
// result is main byte for byte. The guard then holds the whole difference
// from main to the declared paths, each of which passed CheckCodePath.
//
// There is no retry. The branch must still be at s.base, main at
// s.mergeParent, and the push leases the branch tip; any movement is
// ErrConflict (or ErrMergeParent for main) and the caller starts a new round.
func (l *lane) runMerge(ctx context.Context, s snap) (Result, error) {
	if err := os.MkdirAll(l.cfg.WorkDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("mkdir workdir: %w", err)
	}
	tip, err := l.fetch(ctx)
	if err != nil {
		return Result{}, err
	}
	if tip != s.base {
		return Result{Attempts: 1}, fmt.Errorf("%w: the branch moved from %.12s to %.12s", ErrConflict, s.base, tip)
	}
	main := l.cfg.Branch
	if main == "" {
		main = "main"
	}
	spec := "+refs/heads/" + main + ":" + mergeRef
	if err := l.g.FetchRef(ctx, l.cfg.RepoDir, l.cfg.RemoteURL, spec, l.cfg.Token); err != nil {
		return Result{Attempts: 1}, err
	}
	mainTip, err := l.g.RevParse(ctx, l.cfg.RepoDir, mergeRef)
	if err != nil {
		return Result{Attempts: 1}, err
	}
	if mainTip != s.mergeParent {
		return Result{Attempts: 1}, fmt.Errorf("%w: %.12s is not the tip of %s (%.12s)", ErrMergeParent, s.mergeParent, main, mainTip)
	}
	if err := l.g.WorktreeRemove(ctx, l.cfg.RepoDir, l.wt); err != nil {
		return Result{}, err
	}
	if err := l.g.WorktreeAdd(ctx, l.cfg.RepoDir, l.wt, mainTip); err != nil {
		return Result{}, err
	}
	defer func() { _ = l.g.WorktreeRemove(context.WithoutCancel(ctx), l.cfg.RepoDir, l.wt) }()

	res := Result{Attempts: 1}
	if err := l.apply(ctx, s); err != nil {
		return res, err
	}
	// HEAD is main's tip, so the staged diff is the diff against main.
	changes, err := l.g.StagedChanges(ctx, l.wt)
	if err != nil {
		return res, err
	}
	if err := CheckStaged(s.declared, changes); err != nil {
		return res, err
	}
	if err := checkModes(s, changes); err != nil {
		return res, err
	}
	ge := &GuardError{}
	for _, c := range changes { // belt and braces: every difference from main passes the code path guard
		if err := CheckCodePath(s.issue, c.Path); err != nil {
			ge.add(c.Path, err.Error())
		}
	}
	if len(ge.Paths) > 0 {
		return res, ge
	}
	cr, err := l.fx.Commit(ctx, effects.CommitOp{Dir: l.wt, Message: s.msg, Author: l.author, Parents: []string{tip, mainTip}})
	if err != nil {
		return res, err
	}
	err = l.fx.Push(ctx, effects.PushOp{Dir: l.wt, Remote: l.cfg.RemoteURL, Ref: "HEAD:refs/heads/" + l.branch,
		ForceWithLease: tip, Token: l.cfg.Token})
	switch {
	case errors.Is(err, effects.ErrNonFastForward), errors.Is(err, effects.ErrStaleLease):
		return res, fmt.Errorf("%w: the branch moved during the merge: %v", ErrConflict, err)
	case err != nil:
		return res, err
	}
	return Result{SHA: cr.SHA, Synthetic: cr.Synthetic, Attempts: 1}, nil
}
