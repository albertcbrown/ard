package servecmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

// jobLister is the one state read adoption needs; *state.Store satisfies it.
type jobLister interface {
	RunningJobs(ctx context.Context) ([]state.Job, error)
}

// heldJobs maps each running job to a pool.Held so a restart cannot
// over-grant slots. An unknown stage, or a job with no issue or PR number,
// is an error naming the job: serve exits rather than risk over-granting.
func heldJobs(ctx context.Context, l jobLister) ([]pool.Held, error) {
	jobs, err := l.RunningJobs(ctx)
	if err != nil {
		return nil, fmt.Errorf("adopt running jobs: %w", err)
	}
	out := make([]pool.Held, 0, len(jobs))
	for _, j := range jobs {
		if j.Stage == "scanner" {
			continue // phase 0 jobs do not survive a restart; failStaleScannerJobs retires them
		}
		class, err := pool.ParseClass(j.Stage)
		if err != nil {
			return nil, fmt.Errorf("adopt job %s: %w", j.ID, err)
		}
		n := 0
		switch class {
		case pool.ClassReview:
			n = j.PR
		case pool.ClassTriage, pool.ClassWorker:
			n = j.Issue
		}
		if class != pool.ClassScanner && n == 0 {
			return nil, fmt.Errorf("adopt job %s: %s job has no issue or PR number", j.ID, j.Stage)
		}
		out = append(out, pool.Held{
			Candidate: pool.Candidate{Class: class, Role: j.Role, Number: n, Since: j.CreatedAt},
			JobID:     j.ID,
		})
	}
	return out, nil
}

// adoptPool builds the slot pool holding every job already running.
func adoptPool(ctx context.Context, l jobLister, maxPods int, log *slog.Logger) (*pool.Pool, error) {
	held, err := heldJobs(ctx, l)
	if err != nil {
		return nil, err
	}
	log.Info("pool.adopt", "held", len(held), "max", maxPods)
	return pool.New(pool.Options{Max: maxPods, Held: held, Log: log}), nil
}

// scannerJobStore is what retiring stale scanner jobs needs; *state.Store
// satisfies it.
type scannerJobStore interface {
	RunningJobs(ctx context.Context) ([]state.Job, error)
	Fail(ctx context.Context, id, reason string, keepUntil time.Time) error
}

// workspaceCleaner is the executor call used to remove a dead job's workspace.
type workspaceCleaner interface {
	Cleanup(ctx context.Context, h executor.Handle, o executor.CleanupOptions) error
}

// failStaleScannerJobs retires scanner jobs still marked running at startup.
// A phase 0 scan does not survive a restart, and a scanner job adopted as a
// held slot would make every later Offer for its key a duplicate, so the
// scanner would silently never run again. Each job's workspace is cleaned up
// on a best-effort basis, the job is failed, and it is not held. ex may be nil.
func failStaleScannerJobs(ctx context.Context, jobs scannerJobStore, ex workspaceCleaner, log *slog.Logger) error {
	running, err := jobs.RunningJobs(ctx)
	if err != nil {
		return fmt.Errorf("list running scanner jobs: %w", err)
	}
	var errs []error
	for _, j := range running {
		if j.Stage != "scanner" {
			continue
		}
		if ex != nil && len(j.Handle) > 0 {
			var h executor.Handle
			if err := json.Unmarshal(j.Handle, &h); err != nil {
				log.Warn("scanner.stale_job_handle", "job", j.ID, "error", err)
			} else if err := ex.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
				log.Warn("scanner.stale_job_cleanup", "job", j.ID, "error", err)
			}
		}
		if err := jobs.Fail(ctx, j.ID, "controller restarted", time.Time{}); err != nil {
			errs = append(errs, fmt.Errorf("fail stale scanner job %s: %w", j.ID, err))
			continue
		}
		log.Info("scanner.stale_job_failed", "job", j.ID, "scanner", j.Role)
	}
	return errors.Join(errs...)
}
