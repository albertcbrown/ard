package pool

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Verdict is the pool's answer for one candidate.
type Verdict int

// Verdicts. Anything other than Granted means the loop touches no label and
// writes no state; the candidate is offered again next tick.
const (
	Granted         Verdict = iota
	DeniedFull              // no slot was free for it, and no other loop reserved one
	DeniedOutranked         // higher-ranked live demand from another loop reserved the free slots
	DeniedIssueBusy         // triage only: a triage job on the same issue is held or granted earlier in the batch
	DeniedDuplicate         // same key already held, or earlier in this batch
	DeniedInvalid           // malformed candidate (caller bug)
)

var verdictNames = [...]string{"granted", "full", "outranked", "issue-busy", "duplicate", "invalid"}

// String returns the verdict's log name.
func (v Verdict) String() string {
	if v >= 0 && int(v) < len(verdictNames) {
		return verdictNames[v]
	}
	return fmt.Sprintf("verdict(%d)", int(v))
}

// Decision is the result for one offered candidate.
type Decision struct {
	Candidate Candidate
	Verdict   Verdict
	Slot      *Slot  // non-nil if and only if Verdict == Granted
	Reason    string // human-readable, for logs and the status issue
}

// Held is a job already running when the pool is built (restart recovery).
type Held struct {
	Candidate Candidate
	JobID     string
}

// Options configures New.
type Options struct {
	Max         int              // cfg.MaxPods; values < 1 are treated as 1
	Now         func() time.Time // nil means time.Now
	Log         *slog.Logger     // nil means discard
	Held        []Held           // running jobs from state; held even if they exceed Max
	StarveAfter time.Duration    // 0 means 24h
}

// Pool is the in-memory slot arbiter. One mutex guards all state; there are
// no goroutines or timers, and demand expiry is evaluated lazily.
type Pool struct {
	mu          sync.Mutex
	max         int
	now         func() time.Time
	log         *slog.Logger
	starveAfter time.Duration
	held        map[string]*Slot
	demand      map[string]*loopDemand
	firstDenied map[string]time.Time
	starved     map[Class]bool
}

// New builds a pool, adopting o.Held.
func New(o Options) *Pool {
	p := &Pool{
		max:         max(1, o.Max),
		now:         o.Now,
		log:         o.Log,
		starveAfter: o.StarveAfter,
		held:        map[string]*Slot{},
		demand:      map[string]*loopDemand{},
		firstDenied: map[string]time.Time{},
		starved:     map[Class]bool{},
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.log == nil {
		p.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if p.starveAfter == 0 {
		p.starveAfter = 24 * time.Hour
	}
	for _, h := range o.Held {
		k := h.Candidate.Key()
		if p.held[k] != nil {
			p.log.Warn("pool.adopt_duplicate", "key", k, "job_id", h.JobID)
			continue
		}
		p.held[k] = &Slot{pool: p, cand: h.Candidate, jobID: h.JobID, grantedAt: p.now(), adopted: true}
	}
	if len(p.held) > p.max {
		p.log.Warn("pool.adopt_over_max", "held", len(p.held), "max", p.max)
	}
	return p
}

// SetMax changes the cap. It never revokes held slots; n < 1 is treated as 1.
func (p *Pool) SetMax(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.max = max(1, n)
}

// Lookup returns the held slot for key, for close-out and the Reaper after a restart.
func (p *Pool) Lookup(key string) (*Slot, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.held[key]
	return s, ok
}

type item struct {
	cand Candidate
	idx  int // index into decisions; -1 for foreign demand
}

// Offer ranks cands against held slots and other loops' live demand and
// returns one Decision per candidate, in input order. It replaces loop's
// demand entry, which expires ttl after this call (ttl <= 0 records none).
// Loops must call Offer once per tick even with no candidates, so stale
// demand clears.
func (p *Pool) Offer(loop string, ttl time.Duration, cands []Candidate) []Decision {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	p.purgeLocked(now)

	free0 := max(0, p.max-len(p.held))
	free := free0
	decisions := make([]Decision, len(cands))
	triageHeld := map[int]bool{}
	for _, s := range p.held {
		if s.cand.Class == ClassTriage {
			triageHeld[s.cand.Number] = true
		}
	}

	// Validation and duplicates.
	inBatch := map[string]bool{}
	var items []item
	for i, c := range cands {
		decisions[i].Candidate = c
		if why := c.invalid(); why != "" {
			decisions[i].Verdict, decisions[i].Reason = DeniedInvalid, why
			p.log.Warn("pool.invalid", "loop", loop, "class", c.Class.String(), "role", c.Role, "number", c.Number, "reason", why)
			continue
		}
		k := c.Key()
		if p.held[k] != nil || inBatch[k] {
			decisions[i].Verdict, decisions[i].Reason = DeniedDuplicate, "already held or offered earlier in this batch"
			continue
		}
		inBatch[k] = true
		items = append(items, item{c, i})
	}
	for _, c := range p.foreignLocked(loop, now, inBatch, triageHeld) {
		items = append(items, item{c, -1})
	}
	sort.Slice(items, func(a, b int) bool { return less(items[a].cand, items[b].cand) })

	// Walk the merged ranking.
	reserved := 0
	triageGranted := map[int]bool{}
	var denied []Candidate
	capDenied := 0
	for _, it := range items {
		if it.idx < 0 {
			if free > 0 {
				free--
				reserved++
			}
			continue
		}
		d := &decisions[it.idx]
		c := it.cand
		switch {
		case c.Class == ClassTriage && (triageHeld[c.Number] || triageGranted[c.Number]):
			d.Verdict, d.Reason = DeniedIssueBusy, fmt.Sprintf("a triage job on #%d is already held", c.Number)
		case free > 0:
			free--
			s := &Slot{pool: p, cand: c, grantedAt: now}
			p.held[c.Key()] = s
			if c.Class == ClassTriage {
				triageGranted[c.Number] = true
			}
			delete(p.firstDenied, c.Key())
			p.starved[c.Class] = false
			d.Verdict, d.Slot = Granted, s
			p.log.Debug("pool.grant", "loop", loop, "key", c.Key(), "class", c.Class.String(), "role", c.Role, "number", c.Number)
		case capDenied < reserved:
			capDenied++
			d.Verdict, d.Reason = DeniedOutranked, fmt.Sprintf("outranked by %d higher-ranked waiting job(s)", reserved)
			denied = append(denied, c)
		default:
			capDenied++
			d.Verdict, d.Reason = DeniedFull, "no free slot"
			denied = append(denied, c)
		}
	}
	p.setDemandLocked(loop, now, ttl, denied)
	p.checkStarvationLocked(now)

	if p.log.Enabled(context.Background(), slog.LevelDebug) {
		counts := map[string]int{}
		for _, d := range decisions {
			counts[d.Verdict.String()]++
		}
		p.log.Debug("pool.offer", "loop", loop, "verdicts", fmt.Sprint(counts), "free_before", free0, "free_after", max(0, p.max-len(p.held)))
	}
	return decisions
}
