package servecmd

import (
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/digest"
)

// digestLoop posts the daily status-issue digest: queue depth by label, jobs
// run, failures and spend for the previous UTC day.
func digestLoop(w *wiring) controller.Loop {
	return digest.NewLoop(digest.Deps{
		Store: w.Store, Reader: w.Reader, Effects: w.Effects,
		StatusIssue: func() int { return int(w.statusIssue.Load()) },
		Log:         w.Log, Now: w.budgetNow,
	})
}
