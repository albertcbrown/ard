package servecmd

import (
	"context"
	"fmt"
	"log/slog"

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
