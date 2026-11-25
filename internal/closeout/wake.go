package closeout

import (
	"slices"

	"github.com/albertcbrown/ard/internal/labels"
)

// WakeReason is the reason close-out passes to Deps.Wake.
const WakeReason = "closeout"

// WakeTargets names the controller loops that have work after a plan's label
// effects: the next stage the plan requested or unblocked. It is pure.
//
//	ard:worker:<role>            -> workers
//	ard:review:<role>            -> reviewers
//	ard:review:<role>:done       -> merger (the PR may now be ready)
//	ard:review:<role>:changes    -> reviewfix and workers (a review round)
//	ard:triage:<role>, :done, ard:pending -> triage (a request, or the fan-in gate)
//
// Only a plan that finished with outcome done or changes hands off, plus a
// needs-network worker that bounced to the triage engineer (#385: it requests
// ard:triage:engineer); failed, retry, stale, usage-limit and needs-human
// plans, and a needs-network plan that did not bounce, return nil.
func WakeTargets(p Plan) []string {
	switch {
	case p.Outcome == OutcomeDone, p.Outcome == OutcomeChanges:
	case p.Outcome == OutcomeNeedsNetwork && len(p.Requests) > 0:
	default:
		return nil
	}
	var out []string
	add := func(n ...string) {
		for _, x := range n {
			if !slices.Contains(out, x) {
				out = append(out, x)
			}
		}
	}
	for _, l := range slices.Concat(p.Requests, p.Terminal) {
		if l.Name == labels.Pending {
			add("triage")
			continue
		}
		parsed, ok := labels.Parse(l.Name)
		if !ok || parsed.Kind != labels.KindRole {
			continue
		}
		switch parsed.Stage {
		case labels.StageTriage:
			if parsed.State == labels.StateRequest || parsed.State == labels.StateDone {
				add("triage")
			}
		case labels.StageWorker:
			if parsed.State == labels.StateRequest {
				add("workers")
			}
		case labels.StageReview:
			switch parsed.State {
			case labels.StateRequest:
				add("reviewers")
			case labels.StateDone:
				add("merger")
			case labels.StateChanges:
				add("reviewfix", "workers")
			}
		}
	}
	return out
}

// wake tells the controller which loops to run now. It runs after every label
// effect succeeded and never in dry-run (nothing was written, so nothing
// changed for the next stage to see).
func (c *Closer) wake(rec Record) {
	if c.d.Wake == nil || c.d.Effects.DryRun() {
		return
	}
	if names := WakeTargets(rec.Plan); len(names) > 0 {
		c.d.Wake(WakeReason, names...)
	}
}
