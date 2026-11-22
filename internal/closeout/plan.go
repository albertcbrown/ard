package closeout

import (
	"fmt"
	"time"

	"github.com/albertcbrown/ard/internal/labels"
)

// Outcome is what close-out decided about a job.
type Outcome string

const (
	OutcomeDone       Outcome = "done"
	OutcomeChanges    Outcome = "changes"
	OutcomeNeedsHuman Outcome = "needs-human"
	OutcomeFailed     Outcome = "failed"
	// OutcomeRetry is a failure with a named transient cause. The job hands
	// off to one automatic retry: it gets no ard:executor:failed and keeps its
	// request label, so the next claim runs it again as a new job.
	OutcomeRetry Outcome = "retry"
)

// Label is a label name with the issue or PR number it goes on.
type Label struct {
	Number int
	Name   string
}

// FileRef is one file of the commit set. Staged is the host path of the
// cleaned bytes; Resume reads only that, never the worktree.
type FileRef struct {
	Path   string // repo-relative, slash form: .ard/<n>/<stage>-<role>.md
	Staged string
	Delete bool
}

// Plan is everything close-out will do, decided before any side effect.
type Plan struct {
	Outcome     Outcome
	Status      string // raw status of the role file, for the comment header
	Reason      string // failure or withhold reason; never secret text
	Requests    []Label
	Terminal    []Label // added in order
	Remove      []Label // removed in order; Running is last
	Deferred    []NextReq
	Worker      string // the lead's validated worker choice, for the Phase 1 gate
	Skipped     []Skip
	Files       []FileRef
	LogWithheld bool
	LogPath     string // committed log path when it is in Files
	// ExtraLogPaths are the committed paths of Job.ExtraLogs, in order.
	ExtraLogPaths []string
	Summary       string // redacted role file summary, for the comment
	RolePath      string // committed role file path, "" when none

	// Tail is the last lines of the redacted log, for a failed or retry
	// comment. It is "" when there is no log, when the log was withheld, or
	// when the tail did not pass a second redaction (TailWithheld).
	Tail         string
	TailWithheld bool
	// KeepUntil is when the resources of a failed or retry job may be removed
	// (zero on records from before the Reaper: close-out then uses 24 h).
	KeepUntil time.Time
}

// target returns the number labels for this job go on: the PR for a review,
// the issue otherwise.
func (j Job) target() int {
	if j.Stage == StageReview {
		return j.PR
	}
	return j.Issue
}

// Build maps a validated role file to a plan, with no files. It is pure.
func Build(j Job, rf RoleFile, rs RoleSet) Plan {
	p := Plan{Status: rf.Status}
	n := j.target()
	run := Label{n, labels.Running(labels.Stage(j.Stage), j.Role)}
	switch {
	case rf.Status != StatusDone:
		p.Outcome = OutcomeNeedsHuman
		p.Terminal = []Label{{n, labels.NeedsHuman}}
		p.Remove = []Label{run}
	case j.Stage == StageReview && rf.Verdict == VerdictChanges:
		p.Outcome = OutcomeChanges
		p.Terminal = []Label{{n, labels.Changes(j.Role)}}
		p.Remove = []Label{{n, labels.Done(labels.Stage(j.Stage), j.Role)}, run}
	case j.Stage == StageWorker:
		p = buildWorker(j, rf, rs)
	default:
		p.Outcome = OutcomeDone
		p.Terminal = []Label{{n, labels.Done(labels.Stage(j.Stage), j.Role)}}
		if j.Stage == StageReview {
			p.Remove = append(p.Remove, Label{n, labels.Changes(j.Role)})
		}
		if j.Stage == StageTriage && j.Role == "lead" {
			p.Remove = append(p.Remove, Label{n, labels.Pending})
		}
		p.Remove = append(p.Remove, run)
		now, deferred, skipped := resolveNext(j, rf.Next, rs)
		p.Deferred, p.Skipped = deferred, skipped
		for _, r := range now {
			num := j.Issue
			if r.Stage == StageReview {
				num = j.PR
			}
			p.Requests = append(p.Requests, Label{num, labels.Request(labels.Stage(r.Stage), r.Role)})
		}
		if rf.Worker != "" {
			if hasRole(rs, StageWorker, rf.Worker) {
				p.Worker = rf.Worker
			} else {
				p.Skipped = append(p.Skipped, Skip{"worker:" + clip(rf.Worker, 60), "no such role"})
			}
		}
	}
	return p
}

// failedPlan is the plan for a job that did not produce a usable result.
func failedPlan(j Job, reason string, logWithheld bool) Plan {
	n := j.target()
	p := Plan{Outcome: OutcomeFailed, Reason: reason, LogWithheld: logWithheld,
		Terminal: []Label{{n, labels.ExecutorFailed}},
		Remove:   []Label{{n, labels.Running(labels.Stage(j.Stage), j.Role)}}}
	if logWithheld {
		p.Terminal = append(p.Terminal, Label{n, labels.NeedsHuman})
	}
	return p
}

// retryPlan is the plan for a job that failed for a transient cause and hands
// off to one automatic retry. It removes only :running and leaves the request
// label alone: a human who removed it to cancel stays cancelled.
func retryPlan(j Job, reason string) Plan {
	n := j.target()
	st := labels.Stage(j.Stage)
	return Plan{Outcome: OutcomeRetry, Reason: reason,
		Remove: []Label{{n, labels.Running(st, j.Role)}}}
}

// needsHumanPlan is the plan for a job held back by the secret scan.
func needsHumanPlan(j Job, status, reason string) Plan {
	n := j.target()
	return Plan{Outcome: OutcomeNeedsHuman, Status: status, Reason: reason, LogWithheld: false,
		Terminal: []Label{{n, labels.NeedsHuman}},
		Remove:   []Label{{n, labels.Running(labels.Stage(j.Stage), j.Role)}}}
}

func (j Job) validate() error {
	switch j.Stage {
	case StageTriage, StageWorker, StageReview:
	default:
		return fmt.Errorf("closeout job %s: stage %q is not triage, worker or review", j.ID, j.Stage)
	}
	if j.ID == "" || j.Role == "" || j.Issue == 0 {
		return fmt.Errorf("closeout job %q: id, role and issue are required", j.ID)
	}
	if j.Cause.Kind != "" {
		// A job that ended for a known cause commits only its log: it needs
		// neither a base commit nor a branch (an empty branch puts the log on
		// main under .ard/<issue#>/logs/).
		return nil
	}
	if j.ExitCode == 0 && j.BaseSHA == "" {
		return fmt.Errorf("closeout job %s: BaseSHA is required to find the changed files", j.ID)
	}
	if j.Stage == StageReview && j.PR == 0 {
		return fmt.Errorf("closeout job %s: a review needs a PR", j.ID)
	}
	// A worker that stops before its first push has no branch: its role file
	// and log go to main, as triage's do.
	if j.Stage == StageReview && j.Branch == "" {
		return fmt.Errorf("closeout job %s: a review job needs a branch", j.ID)
	}
	return nil
}
