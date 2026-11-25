package servecmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/ui"
)

// approveTimeout bounds one gh call so a hung gh cannot freeze the UI.
const approveTimeout = 30 * time.Second

// approveFn returns the UI's approve action. It runs the operator's own gh
// through the effect layer (dry-run records it instead). On success it wakes
// the triage loop so the gate runs now. When gh is missing or logged out it
// opens the issue page instead. The returned string is the status line.
func approveFn(fx effects.Effects, wake func(reason string, names ...string), repo string, open func(string) error) func(n int) (string, error) {
	return func(n int) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), approveTimeout)
		defer cancel()
		err := fx.ApproveIssue(ctx, repo, n)
		switch {
		case err == nil:
			wake("ui-approve", "triage")
			if fx.DryRun() {
				return fmt.Sprintf("dry-run: would approve #%d", n), nil
			}
			return fmt.Sprintf("approved #%d", n), nil
		case errors.Is(err, effects.ErrGHUnavailable):
			url := ui.IssueURL(repo, n)
			if open != nil {
				_ = open(url)
			}
			return fmt.Sprintf("gh unavailable; add ard:approved at %s", url), nil
		default:
			return "", err
		}
	}
}
