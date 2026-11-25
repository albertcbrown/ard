package servecmd

import (
	"log/slog"
	"sync"
)

// maxSlotsCeiling is the hard upper bound of a runtime slot override.
const maxSlotsCeiling = 16

// slotOverride is the operator's runtime max_pods (#415). It lives in memory
// only; .ard/config.yaml is never written. It holds until max_pods on
// origin/main differs from the value it was set against, then clears.
type slotOverride struct {
	mu   sync.Mutex
	log  *slog.Logger
	n    int // 0 = none
	base int // config max_pods when the override was set
}

func slotLimit(cfgMax int) int { return min(2*cfgMax, maxSlotsCeiling) }

func (s *slotOverride) logf(from, to, cfgMax int, reason string) {
	if s.log != nil {
		s.log.Info("pool.max_override", "from", from, "to", to, "config", cfgMax, "reason", reason)
	}
}

// effectiveLocked returns the cap in force, clearing a stale override.
func (s *slotOverride) effectiveLocked(cfgMax int) int {
	if s.n == 0 {
		return cfgMax
	}
	if s.base != cfgMax {
		s.logf(s.n, cfgMax, cfgMax, "config_changed")
		s.n, s.base = 0, 0
		return cfgMax
	}
	return s.n
}

// Effective is the cap to give the pool for the loaded config max_pods.
func (s *slotOverride) Effective(cfgMax int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.effectiveLocked(cfgMax)
}

// Set overrides the cap, clamped to 1..min(2×config,16), and returns it.
func (s *slotOverride) Set(n, cfgMax int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setLocked(n, cfgMax)
}

func (s *slotOverride) setLocked(n, cfgMax int) int {
	from := s.effectiveLocked(cfgMax)
	n = max(1, min(n, slotLimit(cfgMax)))
	s.n, s.base = n, cfgMax
	if n != from {
		s.logf(from, n, cfgMax, "key")
	}
	return n
}

// Step moves the cap by delta and reports whether it changed.
func (s *slotOverride) Step(delta, cfgMax int) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.effectiveLocked(cfgMax)
	if want := max(1, min(cur+delta, slotLimit(cfgMax))); want == cur {
		return cur, false
	}
	return s.setLocked(cur+delta, cfgMax), true
}

// Active reports whether an override is in force for cfgMax.
func (s *slotOverride) Active(cfgMax int) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.effectiveLocked(cfgMax)
	return n, s.n != 0
}

// initSlots wires the serve logger into the slot override so that
// pool.max_override is logged in a real run.
func (w *wiring) initSlots() { w.slots.log = w.Log }

// reloadMax is the cap for the pool after a config (re)load.
func (w *wiring) reloadMax(cfgMax int) int { return w.slots.Effective(cfgMax) }
