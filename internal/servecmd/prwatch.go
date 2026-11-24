package servecmd

import (
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/prwatch"
)

// prwatchLoop builds the PR watch loop (#267). It needs no executor, Finisher,
// git clone or pool slot: it reads GitHub and the state store, and writes
// labels and comments through the effect layer.
func prwatchLoop(w *wiring) controller.Loop {
	return prwatch.New(prwatch.Deps{
		Reader: w.Reader, Effects: w.Effects, Store: w.Store,
		StatusIssue: func() int { return int(w.statusIssue.Load()) },
		Log:         w.Log,
	})
}
