package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/modelauth"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/reaper"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/state"
)

const (
	defaultInterval = 5 * time.Minute
	defaultPoll     = 2 * time.Second
	stageWorker     = "worker"
	// minStepTime is the least time left that a test or fix step is started with.
	minStepTime = 2 * time.Minute
)

// Reader is the part of github.Reader the loop uses.
type Reader interface {
	Identity(ctx context.Context) (github.Identity, error)
	GetIssue(ctx context.Context, n int) (github.Issue, error)
	SearchIssues(ctx context.Context, q string, o github.SearchOpts) ([]github.Issue, error)
	ListComments(ctx context.Context, n int, since time.Time) ([]github.Comment, error)
	ListPRs(ctx context.Context, o github.ListPRsOpts) ([]github.PullRequest, error)
	ListReviewComments(ctx context.Context, n int) ([]github.ReviewComment, error)
}

// Effects is the part of effects.Effects the loop uses.
type Effects interface {
	AddLabels(ctx context.Context, n int, labels ...string) error
	RemoveLabel(ctx context.Context, n int, label string) error
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
	CreatePR(ctx context.Context, in github.NewPR) (github.PullRequest, error)
	UpdatePR(ctx context.Context, n int, u github.PRUpdate) error
	DryRun() bool
}

// Store is the part of *state.Store the loop uses.
type Store interface {
	CreateJob(ctx context.Context, n state.NewJob) (state.Job, error)
	SetHandle(ctx context.Context, id string, h json.RawMessage) error
	StartAttempt(ctx context.Context, id string) (int, error)
	Fail(ctx context.Context, id, reason string, keepUntil time.Time) error
	Job(ctx context.Context, id string) (state.Job, error)
	RunningJobs(ctx context.Context) ([]state.Job, error)
	Admission(ctx context.Context, issue int) (state.Admission, error)
	SetAdmission(ctx context.Context, a state.Admission) error
	SetBranch(ctx context.Context, id, branch string) error
	SetPR(ctx context.Context, id string, pr int) error
	CloseoutsOf(ctx context.Context, issue int, stage string) ([]state.Job, error)
	Events(ctx context.Context, f state.EventFilter) ([]state.Event, error)
	RecordStepUsage(ctx context.Context, jobID, step string, u state.StepUsage) (bool, error)
}

// Finisher is *reaper.Finisher: the one way a started job ends.
type Finisher interface {
	FinishWith(ctx context.Context, jobID string, h executor.Handle, st executor.Status, res agent.Result, o reaper.FinishOptions) (closeout.Outcome, error)
}

// CodeCommitter is the code lane of *committer.Branches.
type CodeCommitter interface {
	CommitCode(ctx context.Context, branch string, req committer.CodeRequest) (committer.Result, error)
}

// Git is the read side of *gitx.Git. It has no Commit or Push: only
// internal/effects may call those.
type Git interface {
	FetchRef(ctx context.Context, repoDir, url, refspec string, token gitx.TokenFunc) error
	LsRemote(ctx context.Context, dir, url, pattern string, token gitx.TokenFunc) (map[string]string, error)
	Snapshot(ctx context.Context, repoDir, wt, base string, opts ...gitx.SnapshotOptions) (*gitx.Snapshot, error)
	MergeBase(ctx context.Context, repoDir, a, b string) (string, error)
	ChangedPaths(ctx context.Context, dir, from, to string, paths ...string) ([]string, error)
	FileAt(ctx context.Context, dir, rev, path string, maxBytes int64) ([]byte, bool, error)
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
	Code     CodeCommitter
	Git      Git
	Pool     func() Offerer // called at tick time: the pool does not exist when loops are built
	Agents   *agent.Registry
	Parse    reaper.ParseFunc
	// Readings records the usage windows of each agent and fix step, never of
	// a test step. nil records nothing.
	Readings reaper.ReadingRecorder
	Model    modelauth.Credential // zero = none set
	// Secrets returns the known credential values (never nil in use); the
	// diff guard refuses a change that contains one.
	Secrets func() *redact.Secrets
	Scanner redact.Scanner // nil = redact.Builtin()
	// HostPaths are the controller host's paths replaced in the summary that
	// reaches the PR body and the code commit message. Zero = only the job's
	// own paths are replaced.
	HostPaths closeout.HostPaths

	Repo      github.Repo
	RepoDir   string // absolute path of the clone worktrees are cut from
	RemoteURL string
	Token     gitx.TokenFunc
	Poll      time.Duration // Status poll interval; default 2s
	Now       func() time.Time
	Log       *slog.Logger
}

// Loop is the worker loop. It implements controller.Loop.
type Loop struct {
	d Deps

	mu        sync.Mutex
	noCredOK  bool
	noWorkers bool
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
	if d.Scanner == nil {
		d.Scanner = redact.Builtin()
	}
	if d.Secrets == nil {
		d.Secrets = redact.NewSecrets
	}
	return &Loop{d: d}
}

// Name implements controller.Loop.
func (*Loop) Name() string { return "workers" }

// Interval implements controller.Loop.
func (*Loop) Interval(c *config.Resolved) time.Duration {
	if c == nil || c.Intervals.Workers <= 0 {
		return defaultInterval
	}
	return time.Duration(c.Intervals.Workers)
}

// TickTimeout implements controller.TickTimeouter. A tick never waits for a
// job.
func (*Loop) TickTimeout(*config.Resolved) time.Duration { return 5 * time.Minute }

// Claims implements controller.Loop: the loop is skipped while paused.
func (*Loop) Claims() bool { return true }

func (l *Loop) dry() bool { return l.d.Effects.DryRun() }

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
	if cfg == nil || cfg.SHA == "" || len(cfg.RolesOf(config.KindWorker)) == 0 {
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
			log.Warn("worker.no_model_credential", "hint", "agent jobs will not be claimed until one is set")
		}
		l.offer(cfg, nil)
		return nil
	}
	id, err := l.d.Reader.Identity(ctx)
	if err != nil {
		l.offer(cfg, nil)
		return fmt.Errorf("worker: identify github user: %w", err)
	}
	t := &tick{l: l, cfg: cfg, log: log, self: id.Login, dry: dry}
	cands, errs := t.candidates(ctx)

	if dry {
		sort.SliceStable(cands, func(i, j int) bool {
			a, b := cands[i], cands[j]
			if a.priority != b.priority {
				return a.priority < b.priority
			}
			if !a.issue.CreatedAt.Equal(b.issue.CreatedAt) {
				return a.issue.CreatedAt.Before(b.issue.CreatedAt)
			}
			return a.issue.Number < b.issue.Number
		})
		for _, c := range cands {
			log.Info("worker.would_claim", "issue", c.issue.Number, "role", c.role.Name, "priority", c.priority)
		}
		l.offer(cfg, nil)
		return errors.Join(errs...)
	}

	pcs := make([]pool.Candidate, len(cands))
	for i, c := range cands {
		pcs[i] = pool.Candidate{Class: pool.ClassWorker, Role: c.role.Name, Number: c.issue.Number, Since: c.issue.CreatedAt, Priority: c.priority}
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
		name := fmt.Sprintf("worker:%s:%d", c.role.Name, c.issue.Number)
		slot := d.Slot
		if err := env.Inflight.Go(name, func(wctx context.Context) { l.run(wctx, env, cfg, c, slot) }); err != nil {
			slot.Release()
			stopped = true
			errs = append(errs, fmt.Errorf("worker: %w", err))
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

// excluded are the labels that keep an issue out of a worker role's queries.
func excludedLabels(role string) []string {
	return []string{
		labels.Running(labels.StageWorker, role), labels.Done(labels.StageWorker, role),
		labels.ExecutorFailed, labels.NeedsHuman, labels.Hold,
	}
}
