package github

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// Sentinel errors; match with errors.Is.
var (
	ErrNotFound      = errors.New("github: not found")
	ErrUnauthorized  = errors.New("github: unauthorized")
	ErrForbidden     = errors.New("github: forbidden")
	ErrConflict      = errors.New("github: conflict")
	ErrNotMergeable  = errors.New("github: not mergeable")
	ErrReadOnly      = errors.New("github: client is read-only (dry-run)")
	ErrNoCredentials = errors.New("github: no credentials configured")
)

// RateLimitError means GitHub throttled us and the wait was exhausted or
// longer than Config.MaxWait.
type RateLimitError struct {
	ResetAt   time.Time
	Secondary bool
}

func (e *RateLimitError) Error() string {
	kind := "primary"
	if e.Secondary {
		kind = "secondary"
	}
	if e.ResetAt.IsZero() {
		return "github: " + kind + " rate limit exceeded"
	}
	return fmt.Sprintf("github: %s rate limit exceeded, resets at %s", kind, e.ResetAt.UTC().Format(time.RFC3339))
}

// HTTPError is any other failed response.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("github: HTTP %d: %s", e.Status, e.Message)
}

// IsTransient reports whether retrying later may succeed: rate limits, 5xx
// responses and network timeouts. Cancellation is never transient.
func IsTransient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return true
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status >= 500 && he.Status <= 599
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	return false
}
