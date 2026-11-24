package prwatch

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
	"github.com/albertcbrown/ard/internal/prgate"
	"github.com/albertcbrown/ard/internal/state"
)

const (
	// Name is the loop's name, as in --loops and intervals.
	Name = "prwatch"

	defaultInterval       = 5 * time.Minute
	defaultPendingTimeout = 2 * time.Hour
)

// Reader is the part of github.Reader the loop uses.
type Reader interface {
	Identity(ctx context.Context) (github.Identity, error)
	ListPRs(ctx context.Context, o github.ListPRsOpts) ([]github.PullRequest, error)
	GetPR(ctx context.Context, n int) (github.PullRequest, error)
	GetIssue(ctx context.Context, n int) (github.Issue, error)
	ListComments(ctx context.Context, n int, since time.Time) ([]github.Comment, error)
	RequiredChecks(ctx context.Context, branch string) (github.RequiredChecksResult, error)
	ListCheckRuns(ctx context.Context, sha string) ([]github.CheckRun, error)
	ListCommitStatuses(ctx context.Context, sha string) ([]github.CommitStatus, error)
	AppID(ctx context.Context, slug string) (int64, error)
}

// Effects is the part of effects.Effects the loop uses: labels and comments
// only. There is no merge, branch update or review call.
type Effects interface {
	AddLabels(ctx context.Context, n int, labels ...string) error
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
	DryRun() bool
}

// Store is the part of *state.Store the loop uses.
type Store interface {
	RunningJobs(ctx context.Context) ([]state.Job, error)
	AppendEvent(ctx context.Context, e state.Event) error
	Events(ctx context.Context, f state.EventFilter) ([]state.Event, error)
	GetPRWatch(ctx context.Context, pr int) (state.PRWatch, bool, error)
	PutPRWatch(ctx context.Context, w state.PRWatch) error
}

// Deps are the loop's collaborators. StatusIssue returns 0 until the status
// issue is known. Now nil means time.Now; Log nil means slog.Default.
type Deps struct {
	Reader      Reader
	Effects     Effects
	Store       Store
	StatusIssue func() int
	Now         func() time.Time
	Log         *slog.Logger
}

// Loop is the PR watch loop. It implements controller.Loop.
type Loop struct {
	d Deps

	mu   sync.Mutex
	apps map[string]int64      // App slug -> id, per process
	mem  map[int]state.PRWatch // rows, dry run only
	seen map[string]bool       // log lines already written, dry run only
}

var _ controller.Loop = (*Loop)(nil)

// New returns the loop with defaults applied.
func New(d Deps) *Loop {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.StatusIssue == nil {
		d.StatusIssue = func() int { return 0 }
	}
	return &Loop{d: d, apps: map[string]int64{}, mem: map[int]state.PRWatch{}, seen: map[string]bool{}}
}

// Name implements controller.Loop.
func (*Loop) Name() string { return Name }

// Interval implements controller.Loop.
func (*Loop) Interval(c *config.Resolved) time.Duration {
	if c == nil || c.Intervals.PRWatch <= 0 {
		return defaultInterval
	}
	return time.Duration(c.Intervals.PRWatch)
}

// TickTimeout implements controller.TickTimeouter.
func (*Loop) TickTimeout(*config.Resolved) time.Duration { return 5 * time.Minute }

// Claims implements controller.Loop: the loop is skipped while paused. It
// takes no pool slot.
func (*Loop) Claims() bool { return true }

// tick holds what one Tick shares between PRs.
type tick struct {
	l       *Loop
	log     *slog.Logger
	self    string
	dry     bool
	timeout time.Duration
	now     time.Time

	prs      []github.PullRequest
	busy     map[int]bool
	required []github.RequiredCheck
	reqRead  bool
	reqErr   error
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
		return fmt.Errorf("prwatch: identify github user: %w", err)
	}
	timeout := time.Duration(env.Cfg.PRWatch.PendingTimeout)
	if timeout <= 0 {
		timeout = defaultPendingTimeout
	}
	all, err := d.Reader.ListPRs(ctx, github.ListPRsOpts{State: github.StateAll, Base: "main"})
	if err != nil {
		return fmt.Errorf("prwatch: list PRs: %w", err)
	}
	t := &tick{l: l, log: log, self: id.Login, dry: d.Effects.DryRun(), timeout: timeout, now: d.Now()}
	for _, pr := range all {
		if pr.BaseRef != "main" || !strings.EqualFold(pr.Author, t.self) {
			continue
		}
		if _, _, ok := prgate.ParseWorkerBranch(pr.HeadRef); !ok {
			continue
		}
		t.prs = append(t.prs, pr)
	}
	sort.Slice(t.prs, func(i, j int) bool { return t.prs[i].Number < t.prs[j].Number })
	if len(t.prs) == 0 {
		return nil
	}
	jobs, err := d.Store.RunningJobs(ctx)
	if err != nil {
		return fmt.Errorf("prwatch: running jobs: %w", err)
	}
	t.busy = map[int]bool{}
	for _, j := range jobs {
		if j.Stage == "worker" || j.Stage == "review" {
			t.busy[j.Issue] = true
		}
	}
	for _, pr := range t.prs {
		if ctx.Err() != nil {
			break
		}
		select {
		case <-env.Stopping:
			return errors.Join(t.errs...)
		default:
		}
		t.watch(ctx, pr)
	}
	return errors.Join(t.errs...)
}

func (t *tick) fail(pr int, step string, err error) {
	t.log.Warn("prwatch.step_failed", "pr", pr, "step", step, "error", err)
	t.errs = append(t.errs, fmt.Errorf("prwatch PR %d: %s: %w", pr, step, err))
}

// logOnce logs an observation, once per key in a dry run (nothing is stored
// there, so the same PR would repeat it every tick).
func (t *tick) logOnce(key, msg string, args ...any) {
	if t.dry {
		t.l.mu.Lock()
		dup := t.l.seen[key]
		t.l.seen[key] = true
		t.l.mu.Unlock()
		if dup {
			return
		}
	}
	t.log.Info(msg, args...)
}

// requiredChecks reads the rules once per tick, on first use.
func (t *tick) requiredChecks(ctx context.Context) ([]github.RequiredCheck, error) {
	if !t.reqRead {
		t.reqRead = true
		res, err := t.l.d.Reader.RequiredChecks(ctx, "main")
		if err != nil {
			t.reqErr = fmt.Errorf("read required checks: %w", err)
		} else {
			t.required = res.Checks
		}
	}
	return t.required, t.reqErr
}

func (l *Loop) appID(ctx context.Context, slug string) (int64, error) {
	l.mu.Lock()
	id, ok := l.apps[slug]
	l.mu.Unlock()
	if ok {
		return id, nil
	}
	id, err := l.d.Reader.AppID(ctx, slug)
	if err != nil {
		return 0, err
	}
	l.mu.Lock()
	l.apps[slug] = id
	l.mu.Unlock()
	return id, nil
}

func (l *Loop) getRow(ctx context.Context, dry bool, pr int) (state.PRWatch, bool, error) {
	if dry {
		l.mu.Lock()
		defer l.mu.Unlock()
		if w, ok := l.mem[pr]; ok {
			return w, true, nil
		}
	}
	return l.d.Store.GetPRWatch(ctx, pr)
}

func (l *Loop) putRow(ctx context.Context, dry bool, w state.PRWatch) error {
	if dry {
		l.mu.Lock()
		l.mem[w.PR] = w
		l.mu.Unlock()
		return nil
	}
	return l.d.Store.PutPRWatch(ctx, w)
}
