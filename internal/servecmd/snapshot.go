package servecmd

import (
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/snapshot"
)

// snapshotEvents is how many recent log records the command center keeps.
const snapshotEvents = 200

// newSnapshotTaker builds the command center's Snapshot source. The pool and
// the limits book exist only after the startup hooks, so they are looked up
// at Take time; a Take before that sees them as absent. It makes no GitHub
// call: the kill-switch state comes from the controller's last tick.
func (w *wiring) newSnapshotTaker(c *controller.Controller, events *snapshot.Ring) *snapshot.Taker {
	return snapshot.New(snapshot.Sources{
		Pool: func() snapshot.PoolSource {
			if p := w.pool.Load(); p != nil {
				return p
			}
			return nil
		},
		Limits: func() snapshot.LimitSource {
			w.initMu.Lock()
			defer w.initMu.Unlock()
			if w.lim != nil {
				return w.lim
			}
			return nil
		},
		State:   w.Store,
		Control: c,
		Caps:    w.budgetGate(),
		Config:  func() *config.Resolved { return w.cfg.Load() },
		Override: func() (int, bool) {
			c := w.cfg.Load()
			if c == nil {
				return 0, false
			}
			return w.slots.Active(c.MaxPods)
		},
		Ring: events,
	})
}
