package pool

import (
	"sort"
	"time"
)

// loopDemand is one loop's candidates last denied for lack of a slot. It is a
// reservation hint only: a loop that has not offered reserves nothing.
type loopDemand struct {
	expires time.Time
	cands   map[string]Candidate
}

type waiting struct {
	count  int
	oldest time.Duration
	// key and priority belong to the oldest waiter, for the starvation log.
	key      string
	priority int
}

// purgeLocked drops expired demand entries and their first-denied records.
func (p *Pool) purgeLocked(now time.Time) {
	for loop, d := range p.demand {
		if !now.Before(d.expires) {
			for k := range d.cands {
				delete(p.firstDenied, k)
			}
			delete(p.demand, loop)
		}
	}
}

// foreignLocked returns other loops' live demand that is still worth
// reserving: not held now, not in the current batch, and (for triage) whose
// issue has no held triage job.
func (p *Pool) foreignLocked(loop string, now time.Time, batch map[string]bool, triageHeld map[int]bool) []Candidate {
	var out []Candidate
	for l, d := range p.demand {
		if l == loop || !now.Before(d.expires) {
			continue
		}
		for k, c := range d.cands {
			if p.held[k] != nil || batch[k] {
				continue
			}
			if c.Class == ClassTriage && triageHeld[c.Number] {
				continue
			}
			out = append(out, c)
		}
	}
	// Only one triage job per issue can run, so only the best-ranked waiting
	// triage candidate on an issue reserves a slot.
	best := map[int]Candidate{}
	for _, c := range out {
		if c.Class == ClassTriage {
			if b, ok := best[c.Number]; !ok || less(c, b) {
				best[c.Number] = c
			}
		}
	}
	kept := out[:0]
	for _, c := range out {
		if c.Class == ClassTriage && best[c.Number] != c {
			continue
		}
		kept = append(kept, c)
	}
	return kept
}

// setDemandLocked replaces loop's demand entry with denied (empty clears it).
func (p *Pool) setDemandLocked(loop string, now time.Time, ttl time.Duration, denied []Candidate) {
	old := p.demand[loop]
	delete(p.demand, loop)
	d := &loopDemand{expires: now.Add(ttl), cands: map[string]Candidate{}}
	if ttl > 0 {
		for _, c := range denied {
			d.cands[c.Key()] = c
		}
	}
	// Keys that stay keep their first-denied time; keys that dropped out lose it.
	if old != nil {
		for k := range old.cands {
			if _, ok := d.cands[k]; !ok {
				delete(p.firstDenied, k)
			}
		}
	}
	if len(d.cands) == 0 {
		return
	}
	for k := range d.cands {
		if _, ok := p.firstDenied[k]; !ok {
			p.firstDenied[k] = now
		}
	}
	p.demand[loop] = d
}

// waitingLocked summarises live demand per class.
func (p *Pool) waitingLocked(now time.Time) map[Class]waiting {
	out := map[Class]waiting{}
	for _, d := range p.demand {
		if !now.Before(d.expires) {
			continue
		}
		for k, c := range d.cands {
			w := out[c.Class]
			w.count++
			if t, ok := p.firstDenied[k]; ok && now.Sub(t) > w.oldest {
				w.oldest = now.Sub(t)
				w.key, w.priority = k, c.Priority
			}
			out[c.Class] = w
		}
	}
	return out
}

// checkStarvationLocked logs one pool.starved warning when a class's oldest
// wait first crosses StarveAfter. It re-arms once the class has no waiting
// demand or no wait older than StarveAfter. A grant does not clear it, so a
// long-waiting low-priority job does not re-warn on every grant.
func (p *Pool) checkStarvationLocked(now time.Time) {
	w := p.waitingLocked(now)
	for c := ClassReview; c <= ClassScanner; c++ {
		ws, ok := w[c]
		if !ok {
			p.starved[c] = false
			continue
		}
		if ws.oldest < p.starveAfter {
			p.starved[c] = false
			continue
		}
		if !p.starved[c] {
			p.starved[c] = true
			p.log.Warn("pool.starved", "class", c.String(), "waiting", ws.count, "oldest_wait", ws.oldest.String(), "key", ws.key, "priority", ws.priority)
		}
	}
}

// DemandLoops returns the sorted names of loops whose demand is live at now
// and still has a non-scanner candidate that is not held. It does not change state.
func (p *Pool) DemandLoops(now time.Time) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for l, d := range p.demand {
		if !now.Before(d.expires) {
			continue
		}
		for k, c := range d.cands {
			// Scanner runs are never woken by a release; they wait for their schedule.
			if c.Class != ClassScanner && p.held[k] == nil {
				out = append(out, l)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
