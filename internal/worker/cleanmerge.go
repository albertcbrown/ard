package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/gitx"
)

// cleanMergeSummary is the controller-written summary of a clean-merge round.
// No agent text goes into it.
const cleanMergeSummary = "Merged main into the branch. The merge had no conflicts, so no agent ran."

// cleanMergeRound ends a conflict round whose trial merge was clean: the
// controller pushes the merge of main through the diff guard and the code
// lane, then closes the round as done. No agent job is created and nothing is
// spent. Any hold or push failure ends the job the way a failed push does.
func (jb *job) cleanMergeRound(ctx context.Context, cr conflictRound) {
	d := jb.l.d
	n := jb.is.Number
	if _, err := d.Store.StartAttempt(ctx, jb.sj.ID); err != nil {
		jb.log.Error("worker.start_failed", "step", "start_attempt", "error", err)
		jb.finish(ctx, step{}, nil, jb.cause(closeout.CauseNoProcess, "could not start the job", false))
		return
	}
	jb.log.Info("worker.conflict_clean_merge", "branch", jb.branch, "main", cr.mainTip)
	s := step{}
	s.h.Worktree, s.h.BaseSHA = cr.wt, cr.branchTip
	jb.summary = cleanMergeSummary

	stage, err := os.MkdirTemp("", "ard-code-")
	if err == nil {
		defer os.RemoveAll(stage)
		stage, err = filepath.EvalSymlinks(stage)
	}
	if err != nil {
		jb.log.Error("worker.guard_failed", "error", err)
		jb.finish(ctx, step{}, nil, jb.cause(closeout.CauseNoProcess, "the diff guard could not run", false))
		return
	}
	changes, prLines, err := jb.l.facts(ctx, n, cr.wt, cr.mainTip, "", stage)
	var le *gitx.LimitError
	if errors.As(err, &le) {
		jb.log.Info("worker.guard", "hold", true, "limit", le.Error())
		jb.finish(ctx, step{}, &closeout.WorkerResult{Hold: "diff guard: " + Reject{Path: le.Path, Why: le.Why}.String()}, closeout.Cause{})
		return
	}
	if err != nil {
		jb.log.Error("worker.guard_failed", "error", err)
		jb.finish(ctx, step{}, nil, jb.cause(closeout.CauseNoProcess, "the diff guard could not run", false))
		return
	}
	lim := Lines{Warn: jb.role.DiffWarnLines, Max: jb.role.DiffMaxLines}
	v := Check(n, changes, prLines, jb.exists, lim)
	jb.log.Info("worker.guard", "hold", v.Hold != "", "lines", v.Lines, "rejects", len(v.Rejects), "files", len(v.Commit))
	hold := v.Hold
	if hold == "" && v.NoCode {
		// The code lane cannot commit a merge with no files.
		hold = "the merge with main leaves the branch with no changes of its own; a person must decide what to do with it"
	}
	if hold != "" {
		jb.finish(ctx, step{}, &closeout.WorkerResult{Hold: hold}, closeout.Cause{})
		return
	}
	jb.large = v.Large
	if _, ok := jb.push(ctx, s, v); !ok {
		return
	}
	res := closeout.WorkerResult{Tests: closeout.TestsNone, Reopened: true, Summary: cleanMergeSummary}
	if !jb.openPR(ctx, &res) {
		return
	}
	jb.finish(ctx, step{}, &res, closeout.Cause{})
}
