package servecmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agent/claude"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/reaper"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/state"
)

// cfgRoles answers close-out's role questions from the latest loaded config.
type cfgRoles struct{ w *wiring }

func (c cfgRoles) Role(k config.Kind, name string) (config.Role, bool) {
	cfg := c.w.cfg.Load()
	if cfg == nil {
		return config.Role{}, false
	}
	return cfg.Role(k, name)
}

// closer returns the one *closeout.Closer of this process. The triage, worker
// and reviewer loops and the Reaper share it: its per-job busy lock only
// serialises close-outs within one instance.
func (w *wiring) closer() *closeout.Closer {
	w.initMu.Lock()
	defer w.initMu.Unlock()
	if w.cl != nil {
		return w.cl
	}
	staging := filepath.Join(w.StateDir, "closeout")
	if w.StateDir == "" {
		staging = filepath.Join(w.Home, ".ard", "closeout")
	}
	w.cl = closeout.New(closeout.Deps{
		Effects: w.Effects, Comments: w.Reader, Committer: closeoutCommitter{w}, Store: w.Store,
		Roles: cfgRoles{w},
		// The job's model credentials are in the controller's environment.
		NewRedactor: func(closeout.Job) (closeout.Redactor, error) {
			return redact.New(redact.FromEnviron(os.Environ()), redact.Options{})
		},
		Changed:    gitx.New("").ChangedSince,
		Repo:       w.Repo,
		StagingDir: staging,
		Log:        w.Log,
	})
	return w.cl
}

// reaperParts builds, once, the Finisher and Reaper that every loop shares.
func (w *wiring) reaperParts() (*reaper.Finisher, *reaper.Reaper, error) {
	w.initMu.Lock()
	if w.fin != nil {
		defer w.initMu.Unlock()
		return w.fin, w.rp, nil
	}
	w.initMu.Unlock()
	ex, err := w.newExecutor()
	if err != nil {
		return nil, nil, err
	}
	cl := w.closer()
	w.initMu.Lock()
	defer w.initMu.Unlock()
	if w.fin != nil {
		return w.fin, w.rp, nil
	}
	started := w.Started
	if started.IsZero() {
		started = time.Now()
	}
	w.fin = &reaper.Finisher{Closer: cl, Cleaner: ex, Store: w.Store, Start: started, Log: w.Log}
	w.rp = reaper.New(reaper.Deps{
		Store: w.Store, Executor: ex, Finisher: w.fin, Closer: cl,
		Pool:   func() reaper.Lookup { return w.Pool() },
		Reader: w.Reader, Effects: w.Effects, Parse: w.parseFunc(), Log: w.Log,
	})
	return w.fin, w.rp, nil
}

// finisher returns the shared Finisher the loops call when a job's process
// has stopped (see package reaper for the contract).
func (w *wiring) finisher() (*reaper.Finisher, error) {
	f, _, err := w.reaperParts()
	return f, err
}

// reaperLoop returns the Reaper loop, or nil when the executor cannot be built.
func reaperLoop(w *wiring) controller.Loop {
	_, r, err := w.reaperParts()
	if err != nil {
		w.Log.Error("reaper.executor_unavailable", "error", err)
		return nil
	}
	return r
}

// resumeCloseouts is the closeout-resume startup hook. It runs before the pool
// is adopted, so it never touches the pool.
func (w *wiring) resumeCloseouts(ctx context.Context) error {
	_, r, err := w.reaperParts()
	if err != nil {
		// Without an executor nothing runs jobs either; do not keep serve down.
		w.Log.Error("reaper.resume_skipped", "error", err)
		return nil
	}
	return r.ResumeAll(ctx)
}

// parseFunc reads a job's agent result from its stdout log, with the adapter
// its role is configured for.
func (w *wiring) parseFunc() reaper.ParseFunc {
	reg, err := agent.NewRegistry(claude.New(claude.Options{}))
	if err != nil {
		panic(err) // a duplicate adapter name is a programming error
	}
	return func(ctx context.Context, cfg *config.Resolved, sj state.Job, h executor.Handle, exitCode int) (agent.Result, error) {
		if cfg == nil {
			return agent.Result{}, errors.New("no config loaded")
		}
		var kind config.Kind
		switch sj.Stage {
		case closeout.StageTriage:
			kind = config.KindTriage
		case closeout.StageWorker:
			kind = config.KindWorker
		case closeout.StageReview:
			kind = config.KindReviewer
		default:
			return agent.Result{}, fmt.Errorf("stage %q has no agent", sj.Stage)
		}
		role, ok := cfg.Role(kind, sj.Role)
		if !ok {
			return agent.Result{}, fmt.Errorf("role %s/%s is not configured", kind, sj.Role)
		}
		a, err := reg.Get(role.Agent)
		if err != nil {
			return agent.Result{}, err
		}
		if h.StdoutPath == "" {
			return agent.Result{}, errors.New("job has no stdout log")
		}
		f, err := os.Open(h.StdoutPath)
		if err != nil {
			return agent.Result{}, fmt.Errorf("open stdout log: %w", err)
		}
		defer f.Close()
		res, err := a.Parse(f, exitCode)
		if errors.Is(err, agent.ErrNoResult) {
			return res, nil // a killed job has no result event: the partial result is the answer
		}
		return res, err
	}
}
