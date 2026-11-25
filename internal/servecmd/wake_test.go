package servecmd

import "testing"

// Before the controller is bound, a wake is a no-op rather than a panic.
func TestWakeLoopsWithoutControllerIsNoop(t *testing.T) {
	w := &wiring{}
	w.wakeLoops("closeout", "reviewers")
}
