package reaper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

const (
	// pruneEvery and noRecordEvery are in ticks (one tick is a minute).
	pruneEvery    = 60
	noRecordEvery = 5
	// LogRetention is how long host logs stay before Executor.Prune deletes them.
	LogRetention = 7 * 24 * time.Hour
	keepFor      = 24 * time.Hour
	// retentionBatch bounds the jobs cleaned in one tick.
	retentionBatch = 50
)

// Store is the part of state.Store the Reaper uses.
type Store interface {
	FinishStore
	RunningJobs(ctx context.Context) ([]state.Job, error)
	OpenCloseouts(ctx context.Context) ([]state.Job, error)
	SetKillReason(ctx context.Context, id, reason string) error
	Expired(ctx context.Context, now time.Time, limit int) ([]state.Job, error)
	ExtendKeep(ctx context.Context, id string, until time.Time) error
	EndedSince(ctx context.Context, since time.Time) ([]state.Job, error)
	AppendEvent(ctx context.Context, e state.Event) error
}

// Executor is the part of the executor the Reaper uses (*local.Local).
type Executor interface {
	Cleaner
	Status(ctx context.Context, h executor.Handle) (executor.Status, error)
	Prune(ctx context.Context, olderThan time.Duration) error
}

// Lookup finds the held pool slot of a job key (*pool.Pool).
type Lookup interface {
	Lookup(key string) (*pool.Slot, bool)
}

// Reader is the part of github.Reader the Reaper uses.
type Reader interface {
	GetIssue(ctx context.Context, n int) (github.Issue, error)
	ListIssues(ctx context.Context, o github.ListIssuesOpts) ([]github.Issue, error)
	ListComments(ctx context.Context, n int, since time.Time) ([]github.Comment, error)
	ListTimeline(ctx context.Context, n int) ([]github.TimelineEvent, error)
}

// Effects is the part of effects.Effects the Reaper uses: the comment and the
// labels of a :running label with no record. Everything else goes through the
// Closer.
type Effects interface {
	AddLabels(ctx context.Context, n int, labels ...string) error
	RemoveLabel(ctx context.Context, n int, label string) error
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
	DryRun() bool
}

// ParseFunc reads the agent's result for a job from its stdout log. An error
// is logged and the job is treated as having no result.
type ParseFunc func(ctx context.Context, cfg *config.Resolved, sj state.Job, h executor.Handle, exitCode int) (agent.Result, error)

// Deps are the Reaper's collaborators.
type Deps struct {
	Store    Store
	Executor Executor
	Finisher *Finisher
	Closer   Closer
	// Pool returns the slot pool at tick time (it does not exist when loops
	// are constructed). nil means no job is owned.
	Pool    func() Lookup
	Reader  Reader
	Effects Effects
	Parse   ParseFunc
	Log     *slog.Logger
	Now     func() time.Time
}

// Reaper is the controller loop that detects failed jobs. It implements
// controller.Loop and does not claim.
type Reaper struct {
	d     Deps
	start time.Time
	ticks int
}

// New returns a Reaper; its start time is the restart rule's cut-off.
func New(d Deps) *Reaper {
	r := &Reaper{d: d}
	r.start = r.now()
	return r
}

func (r *Reaper) now() time.Time {
	if r.d.Now != nil {
		return r.d.Now()
	}
	return time.Now()
}

func (r *Reaper) log() *slog.Logger {
	if r.d.Log != nil {
		return r.d.Log
	}
	return slog.Default()
}

// Name implements controller.Loop.
func (*Reaper) Name() string { return "reaper" }

// Interval implements controller.Loop.
func (*Reaper) Interval(*config.Resolved) time.Duration { return controller.ReaperInterval }

// TickTimeout lets a tick outlast its interval: kills and close-out pushes can
// take minutes.
func (*Reaper) TickTimeout(*config.Resolved) time.Duration { return 5 * time.Minute }

// Claims is false: the Reaper keeps running while the controller is paused.
func (*Reaper) Claims() bool { return false }

// owned reports whether a goroutine in this process drives sj: its slot is held,
// was not adopted at startup, and is the job's own.
func (r *Reaper) owned(sj state.Job) bool {
	s := r.slot(sj)
	return s != nil && !s.Adopted() && (s.JobID() == "" || s.JobID() == sj.ID)
}

func (r *Reaper) slot(sj state.Job) *pool.Slot {
	if r.d.Pool == nil {
		return nil
	}
	l := r.d.Pool()
	if l == nil {
		return nil
	}
	class, err := pool.ParseClass(sj.Stage)
	if err != nil {
		return nil
	}
	n := sj.Issue
	if sj.Stage == closeout.StageReview {
		n = sj.PR
	}
	s, ok := l.Lookup(pool.KeyFor(class, sj.Role, n))
	if !ok {
		return nil
	}
	return s
}

// releaseAdopted frees the adopted slot of a job the Reaper closed out. A slot
// that belongs to another job, or that a loop owns, is left alone.
func (r *Reaper) releaseAdopted(sj state.Job) {
	if s := r.slot(sj); s != nil && s.Adopted() && (s.JobID() == "" || s.JobID() == sj.ID) {
		s.Release()
	}
}

func (r *Reaper) parse(ctx context.Context, cfg *config.Resolved, sj state.Job, h executor.Handle, exit int) agent.Result {
	if r.d.Parse == nil {
		return agent.Result{}
	}
	res, err := r.d.Parse(ctx, cfg, sj, h, exit)
	if err != nil {
		r.log().Warn("reaper.parse", "job", sj.ID, "error", err)
		return agent.Result{}
	}
	return res
}

// ResumeAll is the serve startup hook. For every open close-out it calls
// ResumeJob and tidies by outcome. It runs before the pool is adopted, so it
// never touches the pool. It logs per-job errors and returns only an error
// listing the close-outs: controller.Run aborts on a hook error, and a GitHub
// 502 must not keep serve down. A job it could not resume stays running; pool
// adoption adopts it and step 1 of the next Tick retries.
func (r *Reaper) ResumeAll(ctx context.Context) error {
	jobs, err := r.d.Store.OpenCloseouts(ctx)
	if err != nil {
		return fmt.Errorf("reaper: list open close-outs: %w", err)
	}
	for _, sj := range jobs {
		if err := r.resume(ctx, sj, false); err != nil {
			r.log().Error("reaper.resume_failed", "job", sj.ID, "issue", sj.Issue, "stage", sj.Stage, "role", sj.Role, "error", err)
		}
	}
	return nil
}

// resume finishes one open close-out and tidies. ErrBusy and a close-out that
// closed meanwhile are not errors.
func (r *Reaper) resume(ctx context.Context, sj state.Job, usePool bool) error {
	out, err := r.d.Closer.ResumeJob(ctx, sj.ID)
	switch {
	case errors.Is(err, closeout.ErrBusy), errors.Is(err, state.ErrNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("reap job %s: resume: %w", sj.ID, err)
	}
	r.log().Info("reaper.resume", "job", sj.ID, "issue", sj.Issue, "stage", sj.Stage, "role", sj.Role, "outcome", string(out))
	h, _ := decodeHandle(sj.Handle)
	r.d.Finisher.Tidy(ctx, sj, h, out)
	if usePool {
		r.releaseAdopted(sj)
	}
	return nil
}

// Tick runs one poll. Every step is best effort per job: errors are collected
// and one bad job never stops the others.
func (r *Reaper) Tick(ctx context.Context, env controller.Env) error {
	r.ticks++
	var errs []error
	errs = append(errs, r.resumeOpen(ctx)...)
	errs = append(errs, r.reapRunning(ctx, env.Cfg)...)
	errs = append(errs, r.retain(ctx)...)
	if r.ticks%pruneEvery == 0 {
		if err := r.d.Executor.Prune(ctx, LogRetention); err != nil {
			errs = append(errs, fmt.Errorf("reaper: prune host logs: %w", err))
		}
	}
	if r.ticks%noRecordEvery == 0 {
		errs = append(errs, r.noRecord(ctx, env.Cfg)...)
	}
	return errors.Join(errs...)
}

// resumeOpen is step 1: finish the close-outs nobody is driving.
func (r *Reaper) resumeOpen(ctx context.Context) []error {
	jobs, err := r.d.Store.OpenCloseouts(ctx)
	if err != nil {
		return []error{fmt.Errorf("reaper: list open close-outs: %w", err)}
	}
	var errs []error
	for _, sj := range jobs {
		if r.owned(sj) {
			continue
		}
		if err := r.resume(ctx, sj, true); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// reapRunning is step 2: decide each running job that has no close-out.
func (r *Reaper) reapRunning(ctx context.Context, cfg *config.Resolved) []error {
	jobs, err := r.d.Store.RunningJobs(ctx)
	if err != nil {
		return []error{fmt.Errorf("reaper: list running jobs: %w", err)}
	}
	var errs []error
	for _, sj := range jobs {
		// The scanner tick and failStaleScannerJobs own scanner jobs, and
		// close-out rejects the scanner stage. A job with a phase is step 1's.
		if sj.Stage == "scanner" || sj.CloseoutPhase != "" {
			continue
		}
		if err := r.reap(ctx, cfg, sj); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (r *Reaper) reap(ctx context.Context, cfg *config.Resolved, sj state.Job) error {
	now := r.now()
	owned := r.owned(sj)
	h, hasHandle := decodeHandle(sj.Handle)
	var st executor.Status
	if hasHandle {
		var err error
		if st, err = r.d.Executor.Status(ctx, h); err != nil {
			return fmt.Errorf("reap job %s: status: %w", sj.ID, err)
		}
	}
	d := decide(owned, hasHandle, h, st, now)
	attrs := []any{"job", sj.ID, "issue", sj.Issue, "stage", sj.Stage, "role", sj.Role}
	switch d.act {
	case actSkip:
		return nil
	case actKillOwned:
		if err := r.kill(ctx, sj, h, string(d.reason)); err != nil {
			return err
		}
		r.log().Warn("reaper.kill", append(attrs, "cause", string(d.reason), "owned", true)...)
		return nil
	case actFinishUnstarted:
		kind, transient := closeout.CauseNoProcess, false
		detail := "never started and its driver is gone"
		if sj.CreatedAt.Before(r.start) {
			kind, transient, detail = closeout.CauseRestart, true, "never started before a controller restart"
		}
		if hasHandle {
			if err := r.d.Executor.Cleanup(ctx, h, executor.CleanupOptions{KeepWorktree: true}); err != nil {
				return fmt.Errorf("reap job %s: cleanup: %w", sj.ID, err)
			}
		}
		return r.finish(ctx, sj, h, st, agent.Result{}, NewCause(sj, kind, detail, transient), attrs)
	case actKillFinish:
		if err := r.d.Executor.Cleanup(ctx, h, executor.CleanupOptions{KeepWorktree: true}); err != nil {
			return fmt.Errorf("reap job %s: kill: %w", sj.ID, err)
		}
		r.log().Warn("reaper.kill", append(attrs, "cause", string(closeout.CauseNoProcess), "owned", false)...)
		return r.finish(ctx, sj, h, st, agent.Result{}, NewCause(sj, closeout.CauseNoProcess, "its driver released the job while it was running", false), attrs)
	case actFinishOrphan:
		if err := r.kill(ctx, sj, h, string(closeout.CauseRestart)); err != nil {
			return err
		}
		res := r.parse(ctx, cfg, sj, h, -1)
		return r.finish(ctx, sj, h, st, res, closeout.Cause{}, attrs)
	default: // actFinishNow
		exit := st.ExitCode
		if st.State == executor.Gone {
			exit = -1
		}
		res := r.parse(ctx, cfg, sj, h, exit)
		return r.finish(ctx, sj, h, st, res, closeout.Cause{}, attrs)
	}
}

// kill records why, then stops the job. A failed kill still keeps the reason:
// the next tick retries.
func (r *Reaper) kill(ctx context.Context, sj state.Job, h executor.Handle, reason string) error {
	if err := r.d.Store.SetKillReason(ctx, sj.ID, reason); err != nil {
		if errors.Is(err, state.ErrNotRunning) {
			return nil // it ended while we looked
		}
		return fmt.Errorf("reap job %s: record kill reason: %w", sj.ID, err)
	}
	if err := r.d.Executor.Cleanup(ctx, h, executor.CleanupOptions{KeepWorktree: true}); err != nil {
		return fmt.Errorf("reap job %s: kill: %w", sj.ID, err)
	}
	return nil
}

// finish closes the job out through the Finisher and releases an adopted slot.
func (r *Reaper) finish(ctx context.Context, sj state.Job, h executor.Handle, st executor.Status, res agent.Result, override closeout.Cause, attrs []any) error {
	out, err := r.d.Finisher.Finish(ctx, sj.ID, h, st, res, override)
	switch {
	case errors.Is(err, closeout.ErrBusy), errors.Is(err, state.ErrNotRunning):
		return nil
	case err != nil:
		return fmt.Errorf("reap job %s: %w", sj.ID, err)
	}
	r.releaseAdopted(sj)
	ev := "reaper.fail"
	switch out {
	case closeout.OutcomeRetry:
		ev = "reaper.retry"
	case closeout.OutcomeDone, closeout.OutcomeChanges, closeout.OutcomeNeedsHuman:
		ev = "reaper.closeout"
	}
	r.log().Info(ev, append(attrs, "outcome", string(out))...)
	return nil
}
