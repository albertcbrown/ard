package closeout

import (
	"fmt"

	"github.com/albertcbrown/ard/internal/state"
)

// CostFacts is what the cost table shows of one closed job. It holds no agent
// text: an outcome and cause word, an exit code, ids and a review mode.
type CostFacts struct {
	Outcome    Outcome
	Cause      CauseKind
	ExitCode   int
	PR         int   // the job's PR, or the one a worker opened; 0 when none
	CommentID  int64 // the posted comment or review; 0 when none
	ReviewMode string
	// Synthetic is true for a dry-run close-out: nothing reached GitHub, so
	// there is nothing to link to.
	Synthetic bool
}

// FactsOf decodes the stored close-out record of a closed job. It is an error
// for a job whose close-out is not closed.
func FactsOf(sj state.Job) (CostFacts, error) {
	rec, err := decode(sj)
	if err != nil {
		return CostFacts{}, err
	}
	if rec.Phase != PhaseClosed {
		return CostFacts{}, fmt.Errorf("closeout job %s: facts: phase %q is not closed", sj.ID, rec.Phase)
	}
	f := CostFacts{Outcome: rec.Plan.Outcome, Cause: rec.Job.Cause.Kind, ExitCode: rec.Job.ExitCode,
		PR: rec.Job.PR, CommentID: rec.CommentID, ReviewMode: rec.ReviewMode, Synthetic: rec.Synthetic}
	if f.PR == 0 && rec.Job.Worker != nil {
		f.PR = rec.Job.Worker.PR
	}
	if f.PR == 0 {
		f.PR = sj.PR
	}
	return f, nil
}
