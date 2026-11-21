// Package effects is the only place that writes to GitHub or git. Effects
// embeds github.Writer unchanged, so the method set covers every GitHub write
// by construction: if the Writer grows a method, both implementations stop
// compiling until they handle it.
//
// Live forwards to the real client and to gitx. DryRun holds no writer at
// all: it logs the full payload, appends it to a JSONL ledger and returns
// synthetic results (negative numbers, see IsSynthetic). Open is the factory;
// it is the only place that builds the GitHub client and it hands everyone
// else a read-only view.
package effects

import (
	"context"
	"errors"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
)

// CommitOp and PushOp are gitx's types. PushOp.ForceWithLease is the expected
// remote SHA (#17 calls it ExpectSHA).
type (
	CommitOp = gitx.CommitOp
	PushOp   = gitx.PushOp
)

// Sentinels re-exported so callers need not import gitx.
var (
	// ErrNothingToCommit is returned by Commit, live and dry-run alike, when
	// the index is unchanged. Callers treat it as a no-op.
	ErrNothingToCommit = gitx.ErrNothingToCommit
	ErrNonFastForward  = gitx.ErrNonFastForward
	ErrStaleLease      = gitx.ErrStaleLease
)

// ErrSyntheticTarget is returned by Live when asked to act on a dry-run
// (negative or zero) issue or PR number.
var ErrSyntheticTarget = errors.New("effects: synthetic dry-run number used on a live target")

// IsSynthetic reports whether n is a dry-run number.
func IsSynthetic(n int) bool { return n < 0 }

// CommitResult is the outcome of Commit. Synthetic is true in dry-run, where
// SHA is a stable hash and no commit exists.
type CommitResult struct {
	SHA       string
	Synthetic bool
}

// Effects is every side effect the controller can cause.
type Effects interface {
	github.Writer
	// UpdateIssueBody replaces an issue body (UpdateIssue with only Body set).
	UpdateIssueBody(ctx context.Context, n int, body string) error
	// Commit commits what is already staged in op.Dir. It returns
	// ErrNothingToCommit when nothing is staged.
	Commit(ctx context.Context, op CommitOp) (CommitResult, error)
	Push(ctx context.Context, op PushOp) error
	// DryRun reports whether effects are only logged.
	DryRun() bool
}

func isNothing(err error) bool { return errors.Is(err, ErrNothingToCommit) }
