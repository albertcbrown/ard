package kube

import (
	"errors"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/kube/api"
)

// obs is everything mapStatus looks at.
type obs struct {
	pod      *api.Pod
	podErr   error
	done     *doneFile
	held     bool // this process runs the watcher
	wtExists bool
	lastOut  time.Time
	now      time.Time
}

// exitTimeout is the exit code of a job that ran out of time (as timeout(1)).
const exitTimeout = 124

// mapStatus is pure: one row per case of the design's table.
func mapStatus(h executor.Handle, o obs) executor.Status {
	st := executor.Status{StartedAt: h.StartedAt, LastOutput: o.lastOut}
	switch {
	case h.Kind != Kind:
		st.State = executor.Gone
		return st
	case h.StartedAt.IsZero():
		st.State = executor.Prepared
		if !o.wtExists {
			st.State = executor.Gone
		}
		return st
	case o.done != nil && o.done.End == "":
		st.State, st.ExitCode, st.Signal, st.FinishedAt = executor.Exited, o.done.Exit, o.done.Signal, o.done.Finished
		if o.done.Exit == exitTimeout || o.done.TimedOut {
			st.State = executor.TimedOut
		}
		return st
	case o.done != nil:
		st.State, st.End, st.Reason, st.FinishedAt = executor.Gone, o.done.End, o.done.Reason, o.done.Finished
		return st
	}
	// A transient API error is not proof that the pod is gone.
	present := o.podErr == nil || !errors.Is(o.podErr, api.ErrNotFound)
	switch {
	case present && !o.held:
		st.State, st.Alive = executor.Orphaned, true
	case present:
		st.State, st.Alive = executor.Running, true
	default:
		st.State, st.End = executor.Gone, executor.EndNoResult
		st.Reason = "pod is gone and no result was recorded"
	}
	return st
}
