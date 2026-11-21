package closeout

import (
	"fmt"
)

// Outcome is what close-out decided about a job.
type Outcome string

const (
	OutcomeDone       Outcome = "done"
	OutcomeChanges    Outcome = "changes"
	OutcomeNeedsHuman Outcome = "needs-human"
	OutcomeFailed     Outcome = "failed"
)

// Label is a label name with the issue or PR number it goes on.
type Label struct {
	Number int
	Name   string
}

// FileRef is one file of the commit set. Staged is the host path of the
// cleaned bytes; Resume reads only that, never the worktree.
type FileRef struct {
	Path   string // repo-relative, slash form: .ard/<n>/<role>.md
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
	Summary     string // redacted role file summary, for the comment
	RolePath    string // committed role file path, "" when none
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
	run := Label{n, labelRunning(j.Stage, j.Role)}
	switch {
	case rf.Status != StatusDone:
		p.Outcome = OutcomeNeedsHuman
		p.Terminal = []Label{{n, labelNeedsHuman}}
		p.Remove = []Label{run}
	case j.Stage == StageReview && rf.Verdict == VerdictChanges:
		p.Outcome = OutcomeChanges
		p.Terminal = []Label{{n, labelChanges(j.Role)}}
		p.Remove = []Label{{n, labelDone(j.Stage, j.Role)}, run}
	default:
		p.Outcome = OutcomeDone
		p.Terminal = []Label{{n, labelDone(j.Stage, j.Role)}}
		if j.Stage == StageReview {
			p.Remove = append(p.Remove, Label{n, labelChanges(j.Role)})
		}
		if j.Stage == StageTriage && j.Role == "lead" {
			p.Remove = append(p.Remove, Label{n, labelPending})
		}
		p.Remove = append(p.Remove, run)
		now, deferred, skipped := resolveNext(j, rf.Next, rs)
		p.Deferred, p.Skipped = deferred, skipped
		for _, r := range now {
			num := j.Issue
			if r.Stage == StageReview {
				num = j.PR
			}
			p.Requests = append(p.Requests, Label{num, labelRequest(r.Stage, r.Role)})
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
		Terminal: []Label{{n, labelExecutorFailed}},
		Remove:   []Label{{n, labelRunning(j.Stage, j.Role)}}}
	if logWithheld {
		p.Terminal = append(p.Terminal, Label{n, labelNeedsHuman})
	}
	return p
}

// needsHumanPlan is the plan for a job held back by the secret scan.
func needsHumanPlan(j Job, status, reason string) Plan {
	n := j.target()
	return Plan{Outcome: OutcomeNeedsHuman, Status: status, Reason: reason, LogWithheld: false,
		Terminal: []Label{{n, labelNeedsHuman}},
		Remove:   []Label{{n, labelRunning(j.Stage, j.Role)}}}
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
	if j.Stage == StageReview && j.PR == 0 {
		return fmt.Errorf("closeout job %s: a review needs a PR", j.ID)
	}
	if j.Stage != StageTriage && j.Branch == "" {
		return fmt.Errorf("closeout job %s: a %s job needs a branch", j.ID, j.Stage)
	}
	return nil
}
