package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
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

	// ErrRepoMoved means the configured repository redirects: it was renamed or
	// transferred. Not transient; point ard at the new name.
	ErrRepoMoved = errors.New("github: repository has moved")
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

// RepoMovedError reports a moved repository. To is zero when GitHub's redirect
// could not be resolved to a name.
type RepoMovedError struct {
	Repo     Repo   // as configured
	To       Repo   // new owner/name, if resolved
	Location string // redirect target path, query stripped
}

func (e *RepoMovedError) Error() string {
	if e.To != (Repo{}) {
		return fmt.Sprintf("github: repository %s has moved to %s; point ard at the new name", e.Repo, e.To)
	}
	return fmt.Sprintf("github: repository %s has moved (redirects to %s); point ard at its new name", e.Repo, e.Location)
}

func (e *RepoMovedError) Unwrap() error { return ErrRepoMoved }

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

// statusError maps a failed response to ard's error vocabulary. Rate limits
// are recognised earlier, in the retry transport.
func statusError(status int, body []byte) error {
	msg := errorMessage(body)
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", ErrUnauthorized, msg)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrForbidden, msg)
	case http.StatusNotFound, http.StatusGone:
		return fmt.Errorf("%w: %s", ErrNotFound, msg)
	}
	return &HTTPError{Status: status, Message: msg}
}

// errorMessage extracts GitHub's {"message": ...} (plus field errors) from a body.
func errorMessage(body []byte) string {
	var v struct {
		Message string `json:"message"`
		Errors  []struct {
			Code    string `json:"code"`
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &v); err != nil || v.Message == "" {
		s := strings.TrimSpace(string(body))
		if len(s) > 200 {
			s = s[:200]
		}
		return s
	}
	msg := v.Message
	for _, e := range v.Errors {
		switch {
		case e.Message != "":
			msg += "; " + e.Message
		case e.Code != "":
			msg += "; " + e.Field + " " + e.Code
		}
	}
	return msg
}
