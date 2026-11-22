package triage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/modelauth"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/reaper"
	"github.com/albertcbrown/ard/internal/state"
)

const (
	defaultInterval = 15 * time.Minute
	defaultPoll     = 2 * time.Second
	stageTriage     = "triage"
	leadRole        = "lead"
)

// Reader is the part of github.Reader the loop uses.
type Reader interface {
	Identity(ctx context.Context) (github.Identity, error)
	GetIssue(ctx context.Context, n int) (github.Issue, error)
	ListIssues(ctx context.Context, o github.ListIssuesOpts) ([]github.Issue, error)
	SearchIssues(ctx context.Context, q string, o github.SearchOpts) ([]github.Issue, error)
	ListComments(ctx context.Context, n int, since time.Time) ([]github.Comment, error)
	ListTimeline(ctx context.Context, n int) ([]github.TimelineEvent, error)
	GetPermission(ctx context.Context, login string) (github.Permission, error)
}

// Effects is the part of effects.Effects the loop uses.
type Effects interface {
	AddLabels(ctx context.Context, n int, labels ...string) error
	RemoveLabel(ctx context.Context, n int, label string) error
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
	DryRun() bool
}

// Store is the part of *state.Store the loop uses.
type Store interface {
	CreateJob(ctx context.Context, n state.NewJob) (state.Job, error)
	SetHandle(ctx context.Context, id string, h json.RawMessage) error
	StartAttempt(ctx context.Context, id string) (int, error)
	Fail(ctx context.Context, id, reason string, keepUntil time.Time) error
	RunningJobs(ctx context.Context) ([]state.Job, error)
	CloseoutsOf(ctx context.Context, issue int, stage string) ([]state.Job, error)
	Admission(ctx context.Context, issue int) (state.Admission, error)
	SetAdmission(ctx context.Context, a state.Admission) error
	AppendEvent(ctx context.Context, e state.Event) error
	Meta(ctx context.Context, key string) (string, error)
	SetMeta(ctx context.Context, key, value string) error
}

// Finisher is *reaper.Finisher: the one way a started job ends.
type Finisher interface {
	Finish(ctx context.Context, jobID string, h executor.Handle, st executor.Status, res agent.Result, override closeout.Cause) (closeout.Outcome, error)
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
	Pool     func() Offerer // called at tick time: the pool does not exist when loops are built
	Agents   *agent.Registry
	Parse    reaper.ParseFunc
	Model    modelauth.Credential // zero = none set
	Repo     string               // "owner/name"
	RepoDir  string               // absolute path of the clone worktrees are cut from
	Poll     time.Duration        // Status poll interval; default 2s
	Now      func() time.Time
	Log      *slog.Logger
}

// Loop is the triage loop. It implements controller.Loop.
type Loop struct {
	d Deps

	mu       sync.Mutex
	noLead   string // config SHA the no-lead warning was last logged for
	noCredOK bool   // the no-credential warning was logged
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
func (*Loop) Name() string { return "triage" }

// Interval implements controller.Loop.
func (*Loop) Interval(c *config.Resolved) time.Duration {
	if c == nil || c.Intervals.Triage <= 0 {
		return defaultInterval
	}
	return time.Duration(c.Intervals.Triage)
}

// TickTimeout implements controller.TickTimeouter. A tick never waits for a
// job, so it needs only enough time for the GitHub reads.
func (*Loop) TickTimeout(*config.Resolved) time.Duration { return 5 * time.Minute }

// Claims implements controller.Loop: the loop is skipped while paused.
func (*Loop) Claims() bool { return true }

func (l *Loop) dry() bool { return l.d.Effects.DryRun() }

// offer reports demand to the pool. It must be called once per tick, with no
// candidates when there is nothing to run, so stale demand clears.
func (l *Loop) offer(cfg *config.Resolved, cands []pool.Candidate) []pool.Decision {
	return l.d.Pool().Offer(l.Name(), 2*l.Interval(cfg), cands)
}

// Tick implements controller.Loop. Errors for one issue are logged and joined,
// so one bad issue never stops the others.
func (l *Loop) Tick(ctx context.Context, env controller.Env) error {
	cfg := env.Cfg
	log := env.Log
	if log == nil {
		log = l.d.Log
	}
	if cfg == nil || cfg.SHA == "" {
		l.offer(cfg, nil)
		return nil
	}
	if _, ok := cfg.Role(config.KindTriage, leadRole); !ok {
		l.mu.Lock()
		first := l.noLead != cfg.SHA
		l.noLead = cfg.SHA
		l.mu.Unlock()
		if first {
			log.Warn("triage.no_lead", "sha", cfg.SHA, "hint", "add .ard/triage/lead/prompt.txt")
		}
		l.offer(cfg, nil)
		return nil
	}
	dry := l.dry()
	if _, err := l.d.Model.Require(); err != nil && !dry {
		l.mu.Lock()
		first := !l.noCredOK
		l.noCredOK = true
		l.mu.Unlock()
		if first {
			log.Warn("triage.no_model_credential", "hint", "agent jobs will not be claimed until one is set")
		}
		l.offer(cfg, nil)
		return nil
	}
	id, err := l.d.Reader.Identity(ctx)
	if err != nil {
		l.offer(cfg, nil)
		return fmt.Errorf("triage: identify github user: %w", err)
	}
	var errs []error
	enabledAt, err := l.enabledAt(ctx, cfg)
	if err != nil {
		errs = append(errs, err)
	}
	t := &tick{l: l, cfg: cfg, log: log, self: id.Login, enabledAt: enabledAt, dry: dry}
	errs = append(errs, t.sweep(ctx)...)
	errs = append(errs, t.fanIn(ctx)...)
	cands, cerrs := t.candidates(ctx)
	errs = append(errs, cerrs...)

	if dry {
		// Dry-run never claims: no job row, no slot, no label.
		for _, c := range cands {
			log.Info("triage.would_claim", "issue", c.issue.Number, "role", c.role.Name, "reason", string(c.reason))
		}
		l.offer(cfg, nil)
		return errors.Join(errs...)
	}

	pcs := make([]pool.Candidate, len(cands))
	for i, c := range cands {
		pcs[i] = pool.Candidate{Class: pool.ClassTriage, Role: c.role.Name, Number: c.issue.Number, Since: c.issue.CreatedAt}
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
		name := fmt.Sprintf("triage:%s:%d", c.role.Name, c.issue.Number)
		slot := d.Slot
		if err := env.Inflight.Go(name, func(wctx context.Context) { l.run(wctx, env, cfg, c, slot) }); err != nil {
			slot.Release()
			stopped = true
			errs = append(errs, fmt.Errorf("triage: %w", err))
		}
	}
	return errors.Join(errs...)
}

// tick holds what one Tick passes between its steps.
type tick struct {
	l         *Loop
	cfg       *config.Resolved
	log       *slog.Logger
	self      string
	enabledAt time.Time
	dry       bool
}

func (t *tick) event(ctx context.Context, issue int, action string, outcome, reason, actor string) {
	data, _ := json.Marshal(map[string]string{"outcome": outcome, "reason": reason, "actor": actor, "action": action})
	if err := t.l.d.Store.AppendEvent(ctx, state.Event{Kind: state.EventIntake, Issue: issue, Stage: stageTriage, Data: data}); err != nil {
		t.log.Warn("triage.event_failed", "issue", issue, "action", action, "error", err)
	}
}

// excluded are the labels that keep an issue out of every triage query.
func excluded(role string) []string {
	return []string{
		labels.Running(labels.StageTriage, role), labels.Done(labels.StageTriage, role),
		labels.ExecutorFailed, labels.NeedsHuman, labels.Hold,
	}
}
