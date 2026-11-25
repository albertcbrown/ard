package executor

import "errors"

var (
	ErrInvalidJob = errors.New("invalid job")
	// ErrImageRef marks an agent image reference that is neither digest-pinned nor a local image ID.
	ErrImageRef       = errors.New("invalid agent image reference")
	ErrExists         = errors.New("job workspace already exists")
	ErrForbiddenEnv   = errors.New("forbidden environment entry")
	ErrNotPrepared    = errors.New("job not prepared")
	ErrAlreadyStarted = errors.New("job already started")
	// ErrClone marks a transient failure to fetch or lay out a clone (for
	// example a sidecar): network, auth or a leftover lock. A ref that does
	// not exist is not ErrClone; retrying it cannot help.
	// ErrInputsTooLarge marks job inputs that do not fit the per-job Secret.
	ErrInputsTooLarge = errors.New("job inputs too large")
	ErrClone          = errors.New("clone or worktree failed")
)

// InputsTooLargeDetail is the close-out detail for ErrInputsTooLarge (the
// jobpod manifest cap, 768 KiB).
const InputsTooLargeDetail = "untrusted inputs exceed 768 KiB"
