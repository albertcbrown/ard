package servecmd

import (
	"time"

	"github.com/albertcbrown/ard/internal/budget"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
)

// budgetGate returns the one daily-budget gate of this process, built on first
// use from the store and the latest loaded config.
func (w *wiring) budgetGate() *budget.Gate {
	w.initMu.Lock()
	defer w.initMu.Unlock()
	if w.bgt == nil {
		now := w.budgetNow
		if now == nil {
			now = time.Now
		}
		w.bgt = budget.New(w.Store, func() *config.Resolved { return w.cfg.Load() }, now, w.Log)
		w.bgt.SetTokenReader(w.Store)
	}
	return w.bgt
}

// budgetLoop is the notice loop: one status-issue comment per capped scope and
// UTC day. It shares the gate, so its caps match what the pool enforces.
func budgetLoop(w *wiring) controller.Loop {
	return budget.NewLoop(budget.Deps{
		Gate: w.budgetGate(), Tokens: w.budgetGate(), Store: w.Store, Effects: w.Effects,
		StatusIssue: func() int { return int(w.statusIssue.Load()) },
		Log:         w.Log, Now: w.budgetNow,
	})
}
