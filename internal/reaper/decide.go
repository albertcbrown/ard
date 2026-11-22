package reaper

import (
	"time"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/executor"
)

const (
	// quietAfter is how long a job may print nothing before the Reaper kills it.
	quietAfter = 10 * time.Minute
	// deadlineSlack is the executor's kill grace (10 s) plus a minute: the
	// Reaper only steps in when the executor's own deadline timer did not.
	deadlineSlack = 71 * time.Second
)

type action int

const (
	actSkip action = iota
	// actKillOwned: a goroutine in this process drives the job. Record the
	// reason and kill; the owner closes out.
	actKillOwned
	// actFinishUnstarted: nobody drives it and it never ran (no handle, or
	// only prepared). Close it out as restart or no_process.
	actFinishUnstarted
	// actKillFinish: nobody drives it but this process still tracks it as
	// running (the driver released its slot). Kill, then close out.
	actKillFinish
	// actFinishNow: the process has stopped; parse its output and close out.
	actFinishNow
	// actFinishOrphan: alive but untracked after a restart. Kill, then close out.
	actFinishOrphan
)

type decision struct {
	act    action
	reason closeout.CauseKind // actKillOwned only
}

// decide is the Reaper's table for one running job with no open close-out. It
// is pure.
func decide(owned, hasHandle bool, h executor.Handle, st executor.Status, now time.Time) decision {
	if owned {
		if !hasHandle || st.State != executor.Running {
			return decision{act: actSkip}
		}
		beat := st.LastOutput
		if h.StartedAt.After(beat) {
			beat = h.StartedAt
		}
		switch {
		case now.Sub(beat) > quietAfter:
			return decision{act: actKillOwned, reason: closeout.CauseQuiet}
		case !h.Deadline.IsZero() && now.After(h.Deadline.Add(deadlineSlack)):
			return decision{act: actKillOwned, reason: closeout.CauseDeadline}
		}
		return decision{act: actSkip}
	}
	if !hasHandle {
		return decision{act: actFinishUnstarted}
	}
	switch st.State {
	case executor.Prepared:
		return decision{act: actFinishUnstarted}
	case executor.Running:
		return decision{act: actKillFinish}
	case executor.Orphaned:
		return decision{act: actFinishOrphan}
	case executor.Exited, executor.TimedOut, executor.Gone:
		return decision{act: actFinishNow}
	}
	return decision{act: actSkip}
}
