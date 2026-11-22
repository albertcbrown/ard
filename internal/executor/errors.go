package executor

import "errors"

var (
	ErrInvalidJob     = errors.New("invalid job")
	ErrExists         = errors.New("job workspace already exists")
	ErrForbiddenEnv   = errors.New("forbidden environment entry")
	ErrNotPrepared    = errors.New("job not prepared")
	ErrAlreadyStarted = errors.New("job already started")
	// ErrClone marks a transient failure to fetch or lay out a clone (for
	// example a sidecar): network, auth or a leftover lock. A ref that does
	// not exist is not ErrClone; retrying it cannot help.
	ErrClone = errors.New("clone or worktree failed")
)
