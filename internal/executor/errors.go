package executor

import "errors"

var (
	ErrInvalidJob     = errors.New("invalid job")
	ErrExists         = errors.New("job workspace already exists")
	ErrForbiddenEnv   = errors.New("forbidden environment entry")
	ErrNotPrepared    = errors.New("job not prepared")
	ErrAlreadyStarted = errors.New("job already started")
)
