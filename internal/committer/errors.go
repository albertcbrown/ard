package committer

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrInvalid is a malformed request.
	ErrInvalid = errors.New("committer: invalid request")
	// ErrGuard is a path outside .ard/<issue#>/ (or an unsafe file kind).
	ErrGuard = errors.New("committer: path guard")
	// ErrConflict means upstream changed one of the request's paths since the
	// base the request was applied to. It is never retried or overwritten.
	ErrConflict = errors.New("committer: upstream changed a requested path")
	// ErrMerge means a File's Merge func rejected the current contents.
	ErrMerge = errors.New("committer: merge failed")
	// ErrBranchGone means the remote branch does not exist.
	ErrBranchGone = errors.New("committer: remote branch does not exist")
	// ErrInitExists means the ard-init branch already exists on the remote, so
	// ard init refuses to propose again.
	ErrInitExists = errors.New("committer: branch ard-init already exists on the remote")
	// ErrTransient means a remote or network failure outlasted the retry
	// budget. It wraps the last cause too.
	ErrTransient = errors.New("committer: transient remote failure persisted")
	// ErrProtected means a push was rejected by branch protection (GH006) and
	// it was not a lost race: a branch lane, or main had not moved since the
	// lease. It is never retried.
	ErrProtected = errors.New("committer: push rejected by branch protection")
	// ErrClosed means Commit was called after Close.
	ErrClosed = errors.New("committer: closed")
)

// GuardError lists every offending path of a request.
type GuardError struct {
	Paths   []string
	Reasons []string
}

func (e *GuardError) Error() string {
	parts := make([]string, len(e.Paths))
	for i := range e.Paths {
		parts[i] = fmt.Sprintf("%q: %s", e.Paths[i], e.Reasons[i])
	}
	return "committer: path guard: " + strings.Join(parts, "; ")
}

func (e *GuardError) Unwrap() error { return ErrGuard }

func (e *GuardError) add(p, reason string) {
	e.Paths = append(e.Paths, p)
	e.Reasons = append(e.Reasons, reason)
}
