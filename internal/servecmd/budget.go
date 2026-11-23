package servecmd

import (
	"time"

	"github.com/albertcbrown/ard/internal/budget"
	"github.com/albertcbrown/ard/internal/config"
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
	}
	return w.bgt
}
