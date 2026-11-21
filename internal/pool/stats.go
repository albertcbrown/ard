package pool

import "time"

// Stats is a point-in-time snapshot; the maps are fresh copies.
type Stats struct {
	Max, Held, Free int
	HeldBy          map[Class]int
	// WaitingBy counts live, ungranted demand across all loops.
	WaitingBy map[Class]int
	// OldestWait is now minus the earliest first-denied time in each class.
	OldestWait map[Class]time.Duration
}

// Stats returns a snapshot of the pool.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	st := Stats{
		Max:        p.max,
		Held:       len(p.held),
		Free:       max(0, p.max-len(p.held)),
		HeldBy:     map[Class]int{},
		WaitingBy:  map[Class]int{},
		OldestWait: map[Class]time.Duration{},
	}
	for _, s := range p.held {
		st.HeldBy[s.cand.Class]++
	}
	for c, n := range p.waitingLocked(now) {
		st.WaitingBy[c] = n.count
		st.OldestWait[c] = n.oldest
	}
	return st
}
