package merger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/state"
)

const (
	defaultInterval = 5 * time.Minute
	defaultNoCI     = 2 * time.Minute
	mainRef         = "refs/ard/merge/main"
)

// Reader is the part of github.Reader the loop uses.
type Reader interface {
	Identity(ctx context.Context) (github.Identity, error)
	ListPRs(ctx context.Context, o github.ListPRsOpts) ([]github.PullRequest, error)
	GetPR(ctx context.Context, n int) (github.PullRequest, error)
	GetIssue(ctx context.Context, n int) (github.Issue, error)
	ListComments(ctx context.Context, n int, since time.Time) ([]github.Comment, error)
	ListReviews(ctx context.Context, n int) ([]github.Review, error)
	GetPermission(ctx context.Context, login string) (github.Permission, error)
	RequiredChecks(ctx context.Context, branch string) (github.RequiredChecksResult, error)
	ListCheckRuns(ctx context.Context, sha string) ([]github.CheckRun, error)
}

// Effects is the part of effects.Effects the loop uses.
type Effects interface {
	AddLabels(ctx context.Context, n int, labels ...string) error
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
	UpdateBranch(ctx context.Context, n int, expectedHeadSHA string) error
	MergePR(ctx context.Context, n int, in github.MergeInput) (github.MergeResult, error)
	DeleteBranch(ctx context.Context, branch string) error
	DryRun() bool
}

// Store is the part of *state.Store the loop uses.
type Store interface {
	CloseoutsOf(ctx context.Context, issue int, stage string) ([]state.Job, error)
	RunningJobs(ctx context.Context) ([]state.Job, error)
	AppendEvent(ctx context.Context, e state.Event) error
	Events(ctx context.Context, f state.EventFilter) ([]state.Event, error)
}

// Git is the read side of *gitx.Git, run against the controller's clone only.
// It has no Commit or Push: only internal/effects may call those.
type Git interface {
	FetchRef(ctx context.Context, repoDir, url, refspec string, token gitx.TokenFunc) error
	RevParse(ctx context.Context, dir, ref string) (string, error)
	MergeBase(ctx context.Context, repoDir, a, b string) (string, error)
	CodeChanged(ctx context.Context, repoDir, from, to string, exclude ...string) ([]string, error)
}

// Deps are the loop's collaborators.
type Deps struct {
	Reader  Reader
	Effects Effects
	Store   Store
	Git     Git

	RepoDir   string // absolute path of the controller's clone
	RemoteURL string
	Token     gitx.TokenFunc
	// NoCIGrace is how long a head must have been known before "no check
	// runs" counts as green; default 2 minutes.
	NoCIGrace time.Duration
	Now       func() time.Time
	Log       *slog.Logger
}

// headSeen is when the loop first saw a PR's current head, and in which tick.
type headSeen struct {
	sha  string
	at   time.Time
	tick uint64
}

// Loop is the merger loop. It implements controller.Loop.
type Loop struct {
	d Deps

	mu    sync.Mutex
	ticks uint64
	heads map[int]headSeen
}

var _ controller.Loop = (*Loop)(nil)

// New returns the loop with defaults applied.
func New(d Deps) *Loop {
	if d.NoCIGrace <= 0 {
		d.NoCIGrace = defaultNoCI
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Loop{d: d, heads: map[int]headSeen{}}
}

// Name implements controller.Loop.
func (*Loop) Name() string { return "merger" }

// Interval implements controller.Loop.
func (*Loop) Interval(c *config.Resolved) time.Duration {
	if c == nil || c.Intervals.Merger <= 0 {
		return defaultInterval
	}
	return time.Duration(c.Intervals.Merger)
}

// TickTimeout implements controller.TickTimeouter.
func (*Loop) TickTimeout(*config.Resolved) time.Duration { return 5 * time.Minute }

// Claims implements controller.Loop: the loop is skipped while paused.
func (*Loop) Claims() bool { return true }

// tick holds what one Tick passes between its steps.
type tick struct {
	l        *Loop
	cfg      *config.Resolved
	log      *slog.Logger
	self     string
	dry      bool
	n        uint64
	stopping <-chan struct{}

	required []github.RequiredCheck // empty: no rules apply
	mainTip  string
	busy     map[int]bool // issues with a running worker or review job
	updated  bool         // one branch update per tick
	errs     []error
}

// Tick implements controller.Loop.
func (l *Loop) Tick(ctx context.Context, env controller.Env) error {
	d := l.d
	log := env.Log
	if log == nil {
		log = d.Log
	}
	if env.Cfg == nil || env.Cfg.SHA == "" {
		return nil
	}
	id, err := d.Reader.Identity(ctx)
	if err != nil {
		return fmt.Errorf("merge: identify github user: %w", err)
	}
	l.mu.Lock()
	l.ticks++
	n := l.ticks
	l.mu.Unlock()
	t := &tick{l: l, cfg: env.Cfg, log: log, self: id.Login, dry: d.Effects.DryRun(), n: n, stopping: env.Stopping}

	prs, err := t.candidates(ctx)
	if err != nil {
		return err
	}
	l.forget(prs)
	if len(prs) == 0 {
		return nil
	}
	if err := t.prepare(ctx); err != nil {
		return err
	}
	for _, pr := range prs {
		if ctx.Err() != nil || t.isStopping() {
			break
		}
		if t.consider(ctx, pr) {
			break // merged: main moved
		}
	}
	return errors.Join(t.errs...)
}

func (t *tick) isStopping() bool {
	select {
	case <-t.stopping:
		return true
	default:
		return false
	}
}

// candidates lists the open worker PRs of the App on main, oldest first.
func (t *tick) candidates(ctx context.Context) ([]github.PullRequest, error) {
	prs, err := t.l.d.Reader.ListPRs(ctx, github.ListPRsOpts{State: github.StateOpen, Base: "main"})
	if err != nil {
		return nil, fmt.Errorf("merge: list PRs: %w", err)
	}
	var out []github.PullRequest
	for _, pr := range prs {
		if pr.State != github.StateOpen || pr.Merged || pr.Draft || pr.BaseRef != "main" || !strings.EqualFold(pr.Author, t.self) {
			continue
		}
		if _, _, ok := parseBranch(pr.HeadRef); !ok {
			continue
		}
		out = append(out, pr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

// forget drops head sightings of PRs that are no longer candidates.
func (l *Loop) forget(prs []github.PullRequest) {
	live := map[int]bool{}
	for _, pr := range prs {
		live[pr.Number] = true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for n := range l.heads {
		if !live[n] {
			delete(l.heads, n)
		}
	}
}

// sawHead records head as PR n's current head and reports whether it has been
// known for at least one earlier tick and the grace period.
func (t *tick) sawHead(n int, head string) (graceOver bool) {
	l := t.l
	now := l.d.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	h, ok := l.heads[n]
	if !ok || h.sha != head {
		l.heads[n] = headSeen{sha: head, at: now, tick: t.n}
		return false
	}
	return h.tick < t.n && now.Sub(h.at) >= l.d.NoCIGrace
}

// prepare reads, once per tick, what every PR shares: the required checks and
// main's tip. An unreadable rules answer merges nothing this tick.
func (t *tick) prepare(ctx context.Context) error {
	d := t.l.d
	res, err := d.Reader.RequiredChecks(ctx, "main")
	if err != nil {
		t.log.Error("merge.required_checks_failed", "error", err)
		return fmt.Errorf("merge: read required checks: %w", err)
	}
	if len(res.Unevaluated) > 0 {
		t.log.Warn("merge.rules_not_evaluated", "rules", strings.Join(res.Unevaluated, ","),
			"hint", "ard reads check runs and required status checks only")
	}
	switch {
	case !res.Available:
		t.log.Debug("merge.no_rules", "hint", "rules API unavailable; merging on reviews and check runs")
	case len(res.Checks) == 0:
		t.log.Debug("merge.no_required_checks", "hint", "no required status checks apply to main; merging on reviews and check runs")
	}
	t.required = res.Checks
	if err := d.Git.FetchRef(ctx, d.RepoDir, d.RemoteURL, "+refs/heads/main:"+mainRef, d.Token); err != nil {
		return fmt.Errorf("merge: fetch main: %w", err)
	}
	if t.mainTip, err = d.Git.RevParse(ctx, d.RepoDir, mainRef); err != nil {
		return fmt.Errorf("merge: resolve main: %w", err)
	}
	return nil
}

func (t *tick) skip(pr int, reason string) {
	t.log.Debug("merge.skip", "pr", pr, "reason", reason)
}

func (t *tick) fail(pr int, step string, err error) {
	t.log.Warn("merge.step_failed", "pr", pr, "step", step, "error", err)
	t.errs = append(t.errs, fmt.Errorf("merge PR %d: %s: %w", pr, step, err))
}
