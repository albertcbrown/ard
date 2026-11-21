package agent

import "errors"

var (
	// ErrInvalidSpec wraps every Spec validation failure.
	ErrInvalidSpec = errors.New("invalid agent spec")
	// ErrNoResult means the stream held no result event.
	ErrNoResult = errors.New("agent produced no result event")
	// ErrUnknownAgent means no adapter is registered under that name.
	ErrUnknownAgent = errors.New("unknown agent")
)
