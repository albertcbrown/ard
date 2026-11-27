package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

// LimitFailer is implemented by a Jobs that can record a usage-limit stop
// (*state.Store does). Without it a limit stop is recorded with Fail.
type LimitFailer interface {
	FailLimited(ctx context.Context, id, reason string, keepUntil time.Time) error
}

// limitStop is a finish error that ends the job record with FailLimited.
type limitStop struct{ err error }

func (e *limitStop) Error() string { return e.err.Error() }
func (e *limitStop) Unwrap() error { return e.err }

// scanJob is one scanner job for runJob.
type scanJob struct {
	role  config.Role
	sha   string
	since time.Time
	// job gets the job record's id and returns the job to prepare.
	job func(id string) executor.Job
	// build turns the prepared handle into the command to start.
	build func(h executor.Handle) (executor.Command, error)
	// finish reads the job's output while the workspace still exists (before
	// Cleanup). Its error decides how the job record ends: nil Complete, a
	// *limitStop FailLimited (when Jobs implements LimitFailer, else Fail),
	// anything else Fail with err.Error(), which must hold no agent text.
	finish func(ctx context.Context, job state.Job, h executor.Handle, st executor.Status) (ScanResult, error)
}

// runJob runs one scanner job: a slot of the scanner class (ErrNoSlot when
// denied; released on every path), a job record, Prepare, Start, WaitFor,
// finish, then Cleanup and the end of the job record. An error before finish
// returns a result holding only the commit.
func runJob(ctx context.Context, d ExecDeps, log *slog.Logger, s scanJob) (res ScanResult, err error) {
	since := s.since
	if since.IsZero() {
		since = d.Now()
	}
	decs := d.Pool().Offer("scanners", d.OfferTTL, []pool.Candidate{{Class: pool.ClassScanner, Role: s.role.Name, Since: since, Priority: labels.DefaultPriority}})
	if len(decs) != 1 || decs[0].Verdict != pool.Granted || decs[0].Slot == nil {
		why := "no decision"
		if len(decs) == 1 {
			why = decs[0].Verdict.String() + ": " + decs[0].Reason
		}
		return ScanResult{}, fmt.Errorf("%w: %s", ErrNoSlot, why)
	}
	slot := decs[0].Slot
	defer slot.Release()

	job, err := d.Jobs.CreateJob(ctx, state.NewJob{Stage: "scanner", Role: s.role.Name})
	if err != nil {
		return ScanResult{SHA: s.sha}, fmt.Errorf("record scanner job: %w", err)
	}
	slot.SetJobID(job.ID)
	defer func() {
		bg := context.WithoutCancel(ctx)
		var jerr error
		var ls *limitStop
		lf, canLimit := d.Jobs.(LimitFailer)
		switch {
		case err == nil:
			jerr = d.Jobs.Complete(bg, job.ID, nil)
		case errors.As(err, &ls) && canLimit:
			jerr = lf.FailLimited(bg, job.ID, err.Error(), time.Time{})
		default:
			jerr = d.Jobs.Fail(bg, job.ID, err.Error(), time.Time{})
		}
		if jerr != nil {
			log.Error("scanner.job_record_failed", "job", job.ID, "error", jerr)
		}
	}()

	h, err := d.Executor.Prepare(ctx, s.job(job.ID))
	if err != nil {
		return ScanResult{SHA: s.sha}, fmt.Errorf("prepare scan: %w", err)
	}
	defer func() {
		if cerr := d.Executor.Cleanup(context.WithoutCancel(ctx), h, executor.CleanupOptions{}); cerr != nil {
			log.Warn("scanner.cleanup_failed", "job", job.ID, "error", cerr)
		}
	}()
	cmd, err := s.build(h)
	if err != nil {
		return ScanResult{SHA: s.sha}, fmt.Errorf("build scan command: %w", err)
	}
	h, err = d.Executor.Start(ctx, h, cmd)
	if err != nil {
		return ScanResult{SHA: s.sha}, fmt.Errorf("start scan: %w", err)
	}
	if b, merr := json.Marshal(h); merr == nil {
		if herr := d.Jobs.SetHandle(ctx, job.ID, b); herr != nil {
			log.Warn("scanner.handle_not_saved", "job", job.ID, "error", herr)
		}
	}
	log.Info("scanner.scan_started", "job", job.ID, "sha", s.sha)

	st, err := executor.WaitFor(ctx, d.Executor, h, d.Poll)
	if err != nil {
		return ScanResult{SHA: s.sha}, fmt.Errorf("wait for scan: %w", err)
	}
	return s.finish(ctx, job, h, st)
}
