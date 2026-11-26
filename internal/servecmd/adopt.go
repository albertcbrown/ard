package servecmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/scanner"
	"github.com/albertcbrown/ard/internal/state"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
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

// adoptPool builds the slot pool holding every job already running. limited,
// when non-nil, is the pool's Options.Limited: it keeps candidates whose
// account or model family is out of quota from starting. budget, when
// non-nil, is the pool's Options.Budget: it refuses candidates at a daily cap.
func adoptPool(ctx context.Context, l jobLister, maxPods int, log *slog.Logger, limited func(pool.Candidate) time.Time, budget func(pool.Candidate) (bool, string)) (*pool.Pool, error) {
	held, err := heldJobs(ctx, l)
	if err != nil {
		return nil, err
	}
	log.Info("pool.adopt", "held", len(held), "max", maxPods)
	return pool.New(pool.Options{Max: maxPods, Held: held, Log: log, Limited: limited, Budget: budget}), nil
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
// A scan does not survive a restart, and a scanner job adopted as a held slot
// would make every later Offer for its key a duplicate, so the scanner would
// silently never run again. Each job's workspace is cleaned up on a
// best-effort basis, the job is failed, and it is not held. ex may be nil.
//
// The interrupted run counts as a run (#204): a scan_failed run starting at
// the job's creation is recorded in the scanner's store, so an LLM scanner
// that a restart interrupts does not spend again at once. A store error is
// logged, never fatal. stores may be nil.
func failStaleScannerJobs(ctx context.Context, jobs scannerJobStore, ex workspaceCleaner, stores scanner.Stores, log *slog.Logger) error {
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
		if stores != nil {
			recordInterruptedScan(ctx, stores, j, log)
		}
	}
	return errors.Join(errs...)
}

// recordInterruptedScan records j, a scanner job a restart interrupted, as a
// scan_failed run of its scanner.
func recordInterruptedScan(ctx context.Context, stores scanner.Stores, j state.Job, log *slog.Logger) {
	st, err := stores(ctx, j.Role)
	if err == nil {
		_, err = st.RecordRun(ctx, scannerdb.Run{StartedAt: j.CreatedAt, FinishedAt: time.Now(), Outcome: scanner.OutcomeScanFailed})
	}
	if err != nil {
		log.Warn("scanner.stale_job_record_failed", "job", j.ID, "scanner", j.Role, "error", err)
	}
}

// sweeper is the executor part that removes pods no job record owns.
type sweeper interface {
	Sweep(ctx context.Context, keep func(id string) bool) error
}

// sweepOrphans deletes this repo's job pods and Secrets that no running job
// owns, when the executor has Sweep (Kubernetes). A pod of a running job is
// left to the Reaper's normal Orphaned path.
func sweepOrphans(ctx context.Context, ex any, store jobLister, log *slog.Logger) error {
	sw, ok := ex.(sweeper)
	if !ok {
		return nil
	}
	jobs, err := store.RunningJobs(ctx)
	if err != nil {
		return fmt.Errorf("sweep orphans: %w", err)
	}
	ids := make([]string, 0, len(jobs))
	for _, j := range jobs {
		ids = append(ids, j.ID)
		if len(j.Handle) > 0 {
			var h executor.Handle
			if json.Unmarshal(j.Handle, &h) == nil && h.JobID != "" {
				ids = append(ids, h.JobID)
			}
		}
	}
	keep := func(id string) bool {
		for _, j := range ids {
			if id == j || agentStepID(j, id) {
				return true
			}
		}
		return false
	}
	if err := sw.Sweep(ctx, keep); err != nil {
		log.Warn("serve.sweep_failed", "error", err)
	}
	return nil
}

// agentStepID reports whether step is a fix step id of job (job + "f" + digits).
func agentStepID(job, step string) bool {
	rest := strings.TrimPrefix(step, job+"f")
	if rest == "" || rest == step {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
