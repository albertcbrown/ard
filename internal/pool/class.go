// Package pool arbitrates the shared pool of job slots (max_pods).
//
// Loops offer candidates once per tick; the pool decides which may start.
// Nothing is queued: candidates are re-derived from GitHub every poll. The
// pool does no I/O, so it sits outside the effect layer and behaves the same
// under --dry-run. It imports only the standard library.
package pool

import "fmt"

// Class is a job's priority class. Lower values fill first. Priority labels
// order candidates within a class only and never cross classes. Nothing
// persists the value, but keep the order: it is the fill order.
type Class int

// Priority classes, highest priority first.
const (
	ClassReview  Class = iota // reviewers fill first
	ClassWorker               // workers
	ClassTriage               // triage
	ClassScanner              // scanners fill last
)

var classNames = [...]string{"review", "worker", "triage", "scanner"}

func (c Class) valid() bool { return c >= ClassReview && c <= ClassScanner }

// String returns "review", "worker", "triage" or "scanner", else "class(N)".
func (c Class) String() string {
	if c.valid() {
		return classNames[c]
	}
	return fmt.Sprintf("class(%d)", int(c))
}

// ParseClass is the inverse of String. The merger and the Reaper take no
// slot, so they have no class and ParseClass rejects them.
func ParseClass(s string) (Class, error) {
	for i, n := range classNames {
		if n == s {
			return Class(i), nil
		}
	}
	return 0, fmt.Errorf("parse pool class %q: unknown class", s)
}

// KeyFor builds the job key "<class>/<role>/<number>", e.g. "triage/lead/42".
func KeyFor(c Class, role string, number int) string {
	return fmt.Sprintf("%s/%s/%d", c, role, number)
}
