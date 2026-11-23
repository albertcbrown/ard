package reviewer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/intake"
	"github.com/albertcbrown/ard/internal/modelauth"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/reaper"
	"github.com/albertcbrown/ard/internal/state"
)

const (
	defaultInterval = 5 * time.Minute
	defaultPoll     = 2 * time.Second
	stageReview     = "review"
)

// Reader is the part of github.Reader the loop uses.
type Reader interface {
	Identity(ctx context.Context) (github.Identity, error)
	GetIssue(ctx context.Context, n int) (github.Issue, error)
	GetPR(ctx context.Context, n int) (github.PullRequest, error)
	SearchIssues(ctx context.Context, q string, o github.SearchOpts) ([]github.Issue, error)
	ListComments(ctx context.Context, n int, since time.Time) ([]github.Comment, error)
}

// Effects is the part of effects.Effects the loop uses.
type Effects interface {
	AddLabels(ctx context.Context, n int, labels ...string) error
	RemoveLabel(ctx context.Context, n int, label string) error
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
	MarkReady(ctx context.Context, n int) error
	DryRun() bool
}

// Store is the part of *state.Store the loop uses.
type Store interface {
	CreateJob(ctx context.Context, n state.NewJob) (state.Job, error)
	SetHandle(ctx context.Context, id string, h json.RawMessage) error
	StartAttempt(ctx context.Context, id string) (int, error)
	Fail(ctx context.Context, id, reason string, keepUntil time.Time) error
	RunningJobs(ctx context.Context) ([]state.Job, error)
	Admission(ctx context.Context, issue int) (state.Admission, error)
	SetAdmission(ctx context.Context, a state.Admission) error
	Events(ctx context.Context, f state.EventFilter) ([]state.Event, error)
}

// Finisher is *reaper.Finisher: the one way a started job ends.
type Finisher interface {
	FinishWith(ctx context.Context, jobID string, h executor.Handle, st executor.Status, res agent.Result, o reaper.FinishOptions) (closeout.Outcome, error)
}

// Git is the read side of *gitx.Git, run against the controller's clone only.
// It has no Commit or Push: only internal/effects may call those.
type Git interface {
	FetchRef(ctx context.Context, repoDir, url, refspec string, token gitx.TokenFunc) error
	RevParse(ctx context.Context, dir, ref string) (string, error)
	MergeBase(ctx context.Context, repoDir, a, b string) (string, error)
	Diff(ctx context.Context, repoDir, from, to string, maxBytes int64, exclude ...string) ([]byte, bool, error)
	AddedLinesBetween(ctx context.Context, repoDir, from, to string, exclude ...string) (map[string][]gitx.LineRange, error)
	CodeChanged(ctx context.Context, repoDir, from, to string, exclude ...string) ([]string, error)
}

// Offerer is *pool.Pool.
type Offerer interface {
	Offer(loop string, ttl time.Duration, cands []pool.Candidate) []pool.Decision
}

// Deps are the loop's collaborators. Executor, Finisher and Parse must be the
// ones the Reaper uses: the Reaper asks the same executor, and the Finisher
// shares the one Closer.
type Deps struct {
	Reader   Reader
	Effects  Effects
	Store    Store
	Executor executor.Executor
	Finisher Finisher
	Git      Git
	Pool     func() Offerer // called at tick time: the pool does not exist when loops are built
	Agents   *agent.Registry
	Parse    reaper.ParseFunc
	Model    modelauth.Credential // zero = none set

	Repo      github.Repo
	RepoDir   string // absolute path of the controller's clone
	RemoteURL string
	Token     gitx.TokenFunc
	Poll      time.Duration // Status poll interval; default 2s
	Now       func() time.Time
	Log       *slog.Logger
}

// Loop is the reviewer loop. It implements controller.Loop.
type Loop struct {
	d Deps

	mu     sync.Mutex
	noCred bool
}

var _ controller.Loop = (*Loop)(nil)

// New returns the loop with defaults applied.
func New(d Deps) *Loop {
	if d.Poll <= 0 {
		d.Poll = defaultPoll
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Loop{d: d}
}

// Name implements controller.Loop.
func (*Loop) Name() string { return "reviewers" }

// Interval implements controller.Loop.
func (*Loop) Interval(c *config.Resolved) time.Duration {
	if c == nil || c.Intervals.Reviewers <= 0 {
		return defaultInterval
	}
	return time.Duration(c.Intervals.Reviewers)
}

// TickTimeout implements controller.TickTimeouter. A tick never waits for a
// job.
func (*Loop) TickTimeout(*config.Resolved) time.Duration { return 5 * time.Minute }

// Claims implements controller.Loop: the loop is skipped while paused.
func (*Loop) Claims() bool { return true }

func (l *Loop) offer(cfg *config.Resolved, cands []pool.Candidate) []pool.Decision {
	return l.d.Pool().Offer(l.Name(), 2*l.Interval(cfg), cands)
}

// Tick implements controller.Loop. Errors for one PR are logged and joined, so
// one bad PR never stops the others.
func (l *Loop) Tick(ctx context.Context, env controller.Env) error {
	cfg := env.Cfg
	log := env.Log
	if log == nil {
		log = l.d.Log
	}
	if cfg == nil || cfg.SHA == "" || len(cfg.RolesOf(config.KindReviewer)) == 0 {
		l.offer(cfg, nil)
		return nil
	}
	dry := l.d.Effects.DryRun()
	id, err := l.d.Reader.Identity(ctx)
	if err != nil {
		l.offer(cfg, nil)
		return fmt.Errorf("review: identify github user: %w", err)
	}
	t := &tick{l: l, cfg: cfg, log: log, self: id.Login, dry: dry}
	// The sweep needs no agent, so it runs before the credential check.
	t.sweepReady(ctx)
	if _, err := l.d.Model.Require(); err != nil && !dry {
		l.mu.Lock()
		first := !l.noCred
		l.noCred = true
		l.mu.Unlock()
		if first {
			log.Warn("review.no_model_credential", "hint", "agent jobs will not be claimed until one is set")
		}
		l.offer(cfg, nil)
		return nil
	}
	cands, errs := t.candidates(ctx)

	if dry {
		for _, c := range cands {
			log.Info("review.would_claim", "pr", c.pr.Number, "issue", c.issue.Number, "role", c.role.Name)
		}
		l.offer(cfg, nil)
		return errors.Join(errs...)
	}

	pcs := make([]pool.Candidate, len(cands))
	for i, c := range cands {
		pcs[i] = pool.Candidate{Class: pool.ClassReview, Role: c.role.Name, Number: c.pr.Number, Since: c.since}
	}
	decs := l.offer(cfg, pcs)
	stopped := false
	for i, d := range decs {
		if d.Verdict != pool.Granted || d.Slot == nil {
			continue
		}
		if stopped {
			d.Slot.Release()
			continue
		}
		c := cands[i]
		name := fmt.Sprintf("review:%s:%d", c.role.Name, c.pr.Number)
		slot := d.Slot
		if err := env.Inflight.Go(name, func(wctx context.Context) { l.run(wctx, env, cfg, c, slot) }); err != nil {
			slot.Release()
			stopped = true
			errs = append(errs, fmt.Errorf("review: %w", err))
		}
	}
	return errors.Join(errs...)
}

// tick holds what one Tick passes between its steps.
type tick struct {
	l    *Loop
	cfg  *config.Resolved
	log  *slog.Logger
	self string
	dry  bool
}

// recheck is the admission re-check a human-filed issue goes through.
func (t *tick) recheck() agentjob.Recheck {
	d := t.l.d
	return agentjob.Recheck{Stage: "review", Stopped: "reviewing", Label: intake.Label(t.cfg.Intake),
		Self: t.self, Dry: t.dry, Log: t.log, Store: d.Store, Reader: d.Reader, Effects: d.Effects}
}
