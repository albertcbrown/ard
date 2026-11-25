// Package snapshot builds the in-process Snapshot behind the `ard serve`
// command center (and a later `ard top`): pool, running jobs, kill switches,
// usage limits, today's issues and spend, and recent log events.
//
// Taking a snapshot is cheap and local. It reads memory (pool, limits book,
// controller) and the SQLite state only; it makes no GitHub call and the
// package must not import internal/github (an import test enforces it).
package snapshot

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/budget"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/limits"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

// Waiting reasons.
const (
	ReasonSlot    = "slot"    // ready, waiting for a free slot
	ReasonLimited = "limited" // account or model family is out of quota
	ReasonBudget  = "budget"  // a daily cap is reached
)

// Snapshot is a point-in-time view. Errors holds one line per source that
// failed; the other fields are still filled.
type Snapshot struct {
	TakenAt time.Time
	DayFrom time.Time // 00:00 UTC of TakenAt's day
	Pool    PoolView
	Running []RunningJob
	Pause   []PauseView
	Ticks   []TickView
	Limits  LimitsView
	Issues  []IssueView
	Queue   []QueueItem // issues waiting for a human to add ard:approved
	Spend   SpendView
	Events  []Event
	Errors  []string
}

// PoolView is the slot pool.
type PoolView struct {
	Used, Max int
	Waiting   []Waiting
}

// Waiting is demand or refused candidates of one role with the reason.
type Waiting struct {
	Class  string
	Role   string
	Reason string // ReasonSlot, ReasonLimited or ReasonBudget
	Count  int
	Oldest time.Duration // slot waits only
}

// RunningJob is one job holding a slot.
type RunningJob struct {
	JobID   string
	Issue   int // 0 for a scanner run
	PR      int
	Stage   string
	Role    string
	Model   string        // from the role config; "" is the agent's default
	Elapsed time.Duration // since the job was created
	Timeout time.Duration // from the role config; 0 when unknown
	// FinishedCostUSD is the cost of finished steps only; nil when no finished
	// step reported a cost. The running step contributes nothing.
	FinishedCostUSD *float64
}

// PauseView is one active kill switch for one claiming loop.
type PauseView struct {
	Loop   string
	Reason controller.Reason
	Err    string
}

// TickView is the last tick of a loop.
type TickView struct {
	Loop       string
	Count      int
	Start, End time.Time
	Err        string
}

// LimitsView holds the usage-limit readings and active blocks.
type LimitsView struct {
	Windows []WindowView
	Blocks  []BlockView
}

// WindowView is the latest reading of one subscription window.
type WindowView struct {
	Window      string
	Utilization float64 // normalised to 0..1
	Status      string
	ResetsAt    time.Time
	Age         time.Duration // now minus when it was observed
}

// BlockView is an active exhaustion block for a model family ("" = unknown).
type BlockView struct {
	Family string
	Until  time.Time
}

// IssueView is an issue with ard activity today.
type IssueView struct {
	Issue   int
	State   string // "<stage>:<role> running" or "<stage>:<role> done|failed" of the latest job
	Jobs    int
	CostUSD *float64 // total of all recorded steps; nil when none priced
}

// QueueItem is an issue in the approval queue: triage finished and the gate
// is waiting for ard:approved. Strings are raw; the UI sanitises them.
type QueueItem struct {
	Issue   int
	Title   string
	Role    string // worker role that starts once approved
	PlanURL string
}

// QueueSource is the in-memory approval queue.
type QueueSource interface {
	Queue() []QueueItem
}

// SpendView is today's spend against the daily caps.
type SpendView struct {
	RepoUSD   float64
	RepoCap   float64 // 0 when no config
	Unpriced  int
	Roles     []RoleSpend
	CappedNow []budget.Cap
}

// RoleSpend is one role's spend today.
type RoleSpend struct {
	Stage, Role string
	USD         float64
}

// PoolSource is the slot pool; *pool.Pool satisfies it.
type PoolSource interface {
	Stats() pool.Stats
	HeldSlots() []pool.HeldInfo
}

// LimitSource is the limits book; *limits.Book satisfies it.
type LimitSource interface {
	Readings() []limits.Reading
	Until(family string) time.Time
}

// StateSource is the state store; *state.Store satisfies it.
type StateSource interface {
	RunningJobs(ctx context.Context) ([]state.Job, error)
	EndedSince(ctx context.Context, since time.Time) ([]state.Job, error)
	JobUsage(ctx context.Context, jobID string) (state.JobUsage, error)
	IssueUsage(ctx context.Context, issue int) (map[string]state.JobUsage, error)
	SpendSince(ctx context.Context, dayStart time.Time) (map[state.RoleKey]state.Spend, state.Spend, error)
}

// ControlSource is the controller; *controller.Controller satisfies it.
type ControlSource interface {
	PauseStates() map[string]controller.PauseState
	Ticks() []controller.TickInfo
}

// CapSource lists the caps reached today; *budget.Gate satisfies it.
type CapSource interface {
	Capped(ctx context.Context) ([]budget.Cap, time.Time, error)
}

// Sources are what a Taker reads. Pool, Limits, Control and Caps may be nil
// (or return nil), for example before startup hooks have run; Config may
// return nil.
type Sources struct {
	Pool      func() PoolSource
	Limits    func() LimitSource
	State     StateSource
	Control   ControlSource
	Caps      CapSource
	Config    func() *config.Resolved
	Ring      *Ring
	Approvals QueueSource // may be nil
	Now       func() time.Time
}

// Taker takes snapshots. It is safe for concurrent use.
type Taker struct {
	s Sources

	mu    sync.Mutex
	cache map[int]cachedUsage
}

type cachedUsage struct {
	key   string
	total *float64
}

// New returns a Taker over s.
func New(s Sources) *Taker {
	if s.Now == nil {
		s.Now = time.Now
	}
	return &Taker{s: s, cache: map[int]cachedUsage{}}
}

// Invalidate drops the cached cost of issue (close-out may call it). The
// cache also invalidates itself when the issue's jobs change.
func (t *Taker) Invalidate(issue int) {
	t.mu.Lock()
	delete(t.cache, issue)
	t.mu.Unlock()
}

// Take builds a snapshot. It makes no GitHub call.
func (t *Taker) Take(ctx context.Context) Snapshot {
	now := t.s.Now().UTC()
	snap := Snapshot{TakenAt: now, DayFrom: time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)}
	cfg := (*config.Resolved)(nil)
	if t.s.Config != nil {
		cfg = t.s.Config()
	}
	fail := func(what string, err error) { snap.Errors = append(snap.Errors, fmt.Sprintf("%s: %v", what, err)) }

	var held []pool.HeldInfo
	if t.s.Pool != nil {
		if p := t.s.Pool(); p != nil {
			snap.Pool, held = poolView(p.Stats()), p.HeldSlots()
			snap.Pool.Used = len(held)
		}
	}
	if t.s.Control != nil {
		snap.Pause = pauseViews(t.s.Control.PauseStates())
		for _, k := range t.s.Control.Ticks() {
			snap.Ticks = append(snap.Ticks, TickView{Loop: k.Loop, Count: k.Count, Start: k.Start, End: k.End, Err: k.Err})
		}
	}
	if t.s.Limits != nil {
		if l := t.s.Limits(); l != nil {
			snap.Limits = limitsView(l, now)
		}
	}
	if t.s.Approvals != nil {
		snap.Queue = t.s.Approvals.Queue()
	}
	if t.s.Ring != nil {
		snap.Events = t.s.Ring.Events()
	}
	if t.s.State == nil {
		return snap
	}

	running, err := t.s.State.RunningJobs(ctx)
	if err != nil {
		fail("running jobs", err)
	}
	snap.Running = t.runningViews(ctx, running, held, cfg, now, fail)

	ended, err := t.s.State.EndedSince(ctx, snap.DayFrom)
	if err != nil {
		fail("ended jobs", err)
	}
	snap.Issues = t.issueViews(ctx, running, ended, fail)

	per, repo, err := t.s.State.SpendSince(ctx, snap.DayFrom)
	if err != nil {
		fail("spend", err)
	} else {
		snap.Spend = spendView(per, repo, cfg)
	}
	if t.s.Caps != nil {
		caps, _, err := t.s.Caps.Capped(ctx)
		if err != nil {
			fail("caps", err)
		}
		snap.Spend.CappedNow = caps
	}
	return snap
}

func poolView(st pool.Stats) PoolView {
	v := PoolView{Used: st.Held, Max: st.Max}
	for _, w := range st.WaitingRoles {
		v.Waiting = append(v.Waiting, Waiting{Class: w.Class.String(), Role: w.Role, Reason: ReasonSlot, Count: w.Count, Oldest: w.Oldest})
	}
	for c, rs := range st.RefusedBy {
		for _, r := range rs {
			reason := ReasonLimited
			if r.Reason == pool.DeniedBudget {
				reason = ReasonBudget
			}
			v.Waiting = append(v.Waiting, Waiting{Class: c.String(), Role: r.Role, Reason: reason, Count: r.Count})
		}
	}
	sort.Slice(v.Waiting, func(i, j int) bool {
		a, b := v.Waiting[i], v.Waiting[j]
		if a.Class != b.Class {
			return a.Class < b.Class
		}
		if a.Role != b.Role {
			return a.Role < b.Role
		}
		return a.Reason < b.Reason
	})
	return v
}

func pauseViews(m map[string]controller.PauseState) []PauseView {
	var out []PauseView
	for loop, st := range m {
		if !st.Paused {
			continue
		}
		v := PauseView{Loop: loop, Reason: st.Reason}
		if st.Err != nil {
			v.Err = st.Err.Error()
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Loop < out[j].Loop })
	return out
}

// NormalizeUtilization maps a reported utilization to 0..1. Streams report
// either a fraction or a percentage; values above 1 are read as percent.
func NormalizeUtilization(u float64) float64 {
	if u > 1 {
		u /= 100
	}
	return math.Min(math.Max(u, 0), 1)
}

func limitsView(l LimitSource, now time.Time) LimitsView {
	var v LimitsView
	for _, r := range l.Readings() {
		v.Windows = append(v.Windows, WindowView{Window: r.Window, Utilization: NormalizeUtilization(r.Utilization),
			Status: r.Status, ResetsAt: r.ResetsAt, Age: max(0, now.Sub(r.ObservedAt))})
	}
	// Until("") is blocked by every entry; named families only by their own
	// and by unknown-family entries, so check each.
	for _, fam := range []string{"opus", "sonnet", "haiku", ""} {
		if u := l.Until(fam); now.Before(u) {
			v.Blocks = append(v.Blocks, BlockView{Family: fam, Until: u})
		}
	}
	return v
}

func (t *Taker) runningViews(ctx context.Context, jobs []state.Job, held []pool.HeldInfo, cfg *config.Resolved, now time.Time, fail func(string, error)) []RunningJob {
	_ = held // the state record is the source of truth; held slots only drive Pool.Used
	var out []RunningJob
	for _, j := range jobs {
		rj := RunningJob{JobID: j.ID, Issue: j.Issue, PR: j.PR, Stage: j.Stage, Role: j.Role, Elapsed: max(0, now.Sub(j.CreatedAt))}
		if cfg != nil {
			for _, r := range cfg.Roles {
				if r.Name == j.Role && stageMatches(r.Kind, j.Stage) {
					rj.Model, rj.Timeout = r.Model, r.Timeout
					break
				}
			}
		}
		u, err := t.s.State.JobUsage(ctx, j.ID)
		if err != nil {
			fail("job usage "+j.ID, err)
		} else {
			rj.FinishedCostUSD = u.CostUSD
		}
		out = append(out, rj)
	}
	return out
}

func stageMatches(k config.Kind, stage string) bool {
	switch k {
	case config.KindScanner:
		return stage == "scanner"
	case config.KindTriage:
		return stage == "triage"
	case config.KindWorker:
		return stage == "worker"
	case config.KindReviewer:
		return stage == "review"
	}
	return false
}

// issueViews lists issues with a running job or a job that ended today (UTC).
func (t *Taker) issueViews(ctx context.Context, running, ended []state.Job, fail func(string, error)) []IssueView {
	type agg struct {
		jobs   int
		latest state.Job
		key    string
	}
	by := map[int]*agg{}
	add := func(j state.Job) {
		if j.Issue == 0 {
			return
		}
		a := by[j.Issue]
		if a == nil {
			a = &agg{}
			by[j.Issue] = a
		}
		a.jobs++
		if j.UpdatedAt.After(a.latest.UpdatedAt) || a.latest.ID == "" {
			a.latest = j
		}
		a.key += fmt.Sprintf("%s@%d;", j.ID, j.UpdatedAt.UnixNano())
	}
	for _, j := range ended {
		add(j)
	}
	for _, j := range running {
		add(j)
	}
	var out []IssueView
	for n, a := range by {
		v := IssueView{Issue: n, Jobs: a.jobs, State: fmt.Sprintf("%s:%s %s", a.latest.Stage, a.latest.Role, a.latest.Status)}
		// Running jobs change cost as steps end without touching the job row,
		// so a running job's issue is never served from the cache.
		cacheable := a.latest.Status != state.StatusRunning
		t.mu.Lock()
		c, hit := t.cache[n]
		t.mu.Unlock()
		if cacheable && hit && c.key == a.key {
			v.CostUSD = c.total
		} else {
			usage, err := t.s.State.IssueUsage(ctx, n)
			if err != nil {
				fail(fmt.Sprintf("issue usage #%d", n), err)
			} else {
				v.CostUSD = sumCost(usage)
				if cacheable {
					t.mu.Lock()
					t.cache[n] = cachedUsage{key: a.key, total: v.CostUSD}
					t.mu.Unlock()
				}
			}
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Issue < out[j].Issue })
	return out
}

func sumCost(m map[string]state.JobUsage) *float64 {
	var total float64
	var any bool
	for _, u := range m {
		if u.CostUSD != nil {
			total += *u.CostUSD
			any = true
		}
	}
	if !any {
		return nil
	}
	return &total
}

func spendView(per map[state.RoleKey]state.Spend, repo state.Spend, cfg *config.Resolved) SpendView {
	v := SpendView{RepoUSD: repo.USD, Unpriced: repo.Unpriced}
	if cfg != nil {
		v.RepoCap = cfg.BudgetUSDPerDay
	}
	for k, s := range per {
		v.Roles = append(v.Roles, RoleSpend{Stage: k.Stage, Role: k.Role, USD: s.USD})
	}
	sort.Slice(v.Roles, func(i, j int) bool {
		if v.Roles[i].Stage != v.Roles[j].Stage {
			return v.Roles[i].Stage < v.Roles[j].Stage
		}
		return v.Roles[i].Role < v.Roles[j].Role
	})
	return v
}
