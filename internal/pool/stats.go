package pool

import (
	"sort"
	"time"
)

// Stats is a point-in-time snapshot; the maps are fresh copies.
type Stats struct {
	Max, Held, Free int
	HeldBy          map[Class]int
	// WaitingBy counts live, ungranted demand across all loops.
	WaitingBy map[Class]int
	// OldestWait is now minus the earliest first-denied time in each class.
	OldestWait map[Class]time.Duration
	// RefusedBy lists recent limited and budget refusals per class. These
	// candidates reserve no demand, so they are not in WaitingBy.
	RefusedBy map[Class][]Refusal
	// WaitingRoles breaks WaitingBy down by role, sorted by class then role.
	WaitingRoles []RoleWait
}

// RoleWait is the live slot demand of one role.
type RoleWait struct {
	Class  Class
	Role   string
	Count  int
	Oldest time.Duration
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
		RefusedBy:  map[Class][]Refusal{},
	}
	st.WaitingRoles = p.waitingRolesLocked(now)
	for c, rs := range p.refusalsLocked(now) {
		st.RefusedBy[c] = rs
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

// refusalTTL is how long a limited or budget refusal is reported after the
// candidate was last refused. Loops offer every tick, so a live refusal is
// refreshed well inside it.
const refusalTTL = 2 * time.Minute

// Refusal aggregates the live limited or budget refusals of one role. Such a
// candidate takes no slot and reserves no demand; this is for display only
// and never feeds a grant decision.
type Refusal struct {
	Role   string
	Reason Verdict // DeniedLimited or DeniedBudget
	Count  int     // distinct candidates refused
	Since  time.Time
}

type refusalKey struct {
	cand    string
	verdict Verdict
}

type refusal struct {
	class        Class
	role         string
	first, taken time.Time
}

// noteRefusalLocked records that c was refused for v at now.
func (p *Pool) noteRefusalLocked(c Candidate, v Verdict, now time.Time) {
	k := refusalKey{c.Key(), v}
	if r := p.refused[k]; r != nil {
		r.taken = now
		return
	}
	p.refused[k] = &refusal{class: c.Class, role: c.Role, first: now, taken: now}
}

// refusalsLocked returns the live refusals per class, sorted by role then reason.
func (p *Pool) refusalsLocked(now time.Time) map[Class][]Refusal {
	type agg struct {
		class Class
		role  string
		v     Verdict
	}
	got := map[agg]*Refusal{}
	for k, r := range p.refused {
		if now.Sub(r.taken) >= refusalTTL {
			continue
		}
		a := agg{r.class, r.role, k.verdict}
		g := got[a]
		if g == nil {
			g = &Refusal{Role: r.role, Reason: k.verdict, Since: r.first}
			got[a] = g
		}
		g.Count++
		if r.first.Before(g.Since) {
			g.Since = r.first
		}
	}
	out := map[Class][]Refusal{}
	for a, g := range got {
		out[a.class] = append(out[a.class], *g)
	}
	for _, rs := range out {
		sort.Slice(rs, func(i, j int) bool {
			if rs[i].Role != rs[j].Role {
				return rs[i].Role < rs[j].Role
			}
			return rs[i].Reason < rs[j].Reason
		})
	}
	return out
}

// HeldInfo describes one held slot.
type HeldInfo struct {
	Key       string
	Candidate Candidate
	JobID     string
	GrantedAt time.Time
	Adopted   bool
}

// HeldSlots lists the held slots, sorted by grant time then key.
func (p *Pool) HeldSlots() []HeldInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]HeldInfo, 0, len(p.held))
	for k, s := range p.held {
		out = append(out, HeldInfo{Key: k, Candidate: s.cand, JobID: s.jobID, GrantedAt: s.grantedAt, Adopted: s.adopted})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].GrantedAt.Equal(out[j].GrantedAt) {
			return out[i].GrantedAt.Before(out[j].GrantedAt)
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func (p *Pool) waitingRolesLocked(now time.Time) []RoleWait {
	type rk struct {
		c    Class
		role string
	}
	got := map[rk]*RoleWait{}
	for _, d := range p.demand {
		if !now.Before(d.expires) {
			continue
		}
		for k, c := range d.cands {
			g := got[rk{c.Class, c.Role}]
			if g == nil {
				g = &RoleWait{Class: c.Class, Role: c.Role}
				got[rk{c.Class, c.Role}] = g
			}
			g.Count++
			if t, ok := p.firstDenied[k]; ok {
				g.Oldest = max(g.Oldest, now.Sub(t))
			}
		}
	}
	out := make([]RoleWait, 0, len(got))
	for _, g := range got {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Class != out[j].Class {
			return out[i].Class < out[j].Class
		}
		return out[i].Role < out[j].Role
	})
	return out
}
