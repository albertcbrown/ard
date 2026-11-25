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
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/limits"
	"github.com/albertcbrown/ard/internal/pool"
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

// gitleaksOptions reports whether the optional gitleaks pass is enabled in the
// latest loaded config, and which binary to run.
func (w *wiring) gitleaksOptions() (redact.GitleaksOptions, bool) {
	cfg := w.cfg.Load()
	if cfg == nil || !cfg.Redact.Gitleaks.Enabled {
		return redact.GitleaksOptions{}, false
	}
	return redact.GitleaksOptions{Path: cfg.Redact.Gitleaks.Path}, true
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
		staging = filepath.Join(w.ardRoot(), "closeout")
	}
	var getPR func(context.Context, int) (github.PullRequest, error)
	if w.Reader != nil {
		getPR = w.Reader.GetPR
	}
	// Network switches the network bounce (#385) on: a needs-network worker is
	// sent to the triage engineer, which re-arms the lead (#386).
	w.cl = closeout.New(closeout.Deps{
		Effects: w.Effects, Comments: w.Reader, Committer: closeoutCommitter{w}, Store: w.Store,
		Roles: cfgRoles{w}, Network: w.Store,
		// The job's model credentials are in the controller's environment.
		// Host paths are replaced by placeholders in the same pass.
		NewRedactor: closeout.JobRedactor(os.Environ, w.hostPaths(), w.gitleaksOptions),
		// Job worktrees belong to the agent: git runs in the controller's own
		// clone (w.Root) and never trusts a worktree's .git, index or config.
		Changed: func(ctx context.Context, worktree, base string, paths ...string) ([]gitx.PathChange, error) {
			return gitx.New("").ChangedSince(ctx, w.Root, worktree, base, paths...)
		},
		BaseFile: func(ctx context.Context, _, rev, path string, maxBytes int64) ([]byte, bool, error) {
			return gitx.New("").FileAt(ctx, w.Root, rev, path, maxBytes)
		},
		PR:         getPR,
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
	// With no credential Auth is "": only the rate_limit_event and limit-message
	// signals count as a usage limit.
	w.fin = &reaper.Finisher{Closer: cl, Cleaner: ex, Store: w.Store, Start: started, Log: w.Log,
		Classify: reaper.UsageLimitClassifier(w.Model.Auth), Limits: lazyLimits{w}, Readings: readingObserver{w}}
	w.rp = reaper.New(reaper.Deps{
		Store: w.Store, Executor: ex, Finisher: w.fin, Closer: cl,
		Pool:   func() reaper.Lookup { return w.Pool() },
		Reader: w.Reader, Effects: w.Effects, Parse: w.parseFunc(), Log: w.Log,
	})
	return w.fin, w.rp, nil
}

// lazyLimits is the Finisher's LimitRecorder. The book opens on first use (the
// closeout-resume hook opens it at startup and fails serve on a read error), so
// building the Finisher needs no state store.
type lazyLimits struct{ w *wiring }

func (l lazyLimits) Exhaust(ctx context.Context, family string, resetsAt time.Time) (time.Time, error) {
	b, err := l.w.limitsBook(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return b.Exhaust(ctx, family, resetsAt)
}

func (l lazyLimits) Succeeded(ctx context.Context, family string) error {
	b, err := l.w.limitsBook(ctx)
	if err != nil {
		return err
	}
	return b.Succeeded(ctx, family)
}

// limitsBook returns the one limits book of this process, opened on first use.
// An open error is a real store read failure (a corrupt value only logs), so
// it is returned and not cached.
func (w *wiring) limitsBook(ctx context.Context) (*limits.Book, error) {
	w.initMu.Lock()
	defer w.initMu.Unlock()
	if w.lim != nil {
		return w.lim, nil
	}
	b, err := limits.Open(ctx, w.Store, time.Now, w.Log)
	if err != nil {
		return nil, fmt.Errorf("serve: %w", err)
	}
	w.lim = b
	return b, nil
}

// limitedFor is the pool's Options.Limited: when a candidate may next start
// given the usage limits recorded so far. Deterministic scanners use no model
// and are never limited. A role missing from the config is never offered, so
// it is not limited either.
func (w *wiring) limitedFor(c pool.Candidate) time.Time {
	var kind config.Kind
	switch c.Class {
	case pool.ClassScanner:
		kind = config.KindScanner
	case pool.ClassTriage:
		kind = config.KindTriage
	case pool.ClassWorker:
		kind = config.KindWorker
	case pool.ClassReview:
		kind = config.KindReviewer
	default:
		return time.Time{}
	}
	cfg := w.cfg.Load()
	if cfg == nil {
		return time.Time{}
	}
	role, ok := cfg.Role(kind, c.Role)
	if !ok || (kind == config.KindScanner && role.ScanPath != "") {
		return time.Time{}
	}
	w.initMu.Lock()
	book := w.lim
	w.initMu.Unlock()
	if book == nil {
		return time.Time{}
	}
	return book.Until(limits.FamilyOf(role.Model))
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
	// A store that cannot be read means state is broken: fail startup.
	if _, err := w.limitsBook(ctx); err != nil {
		return fmt.Errorf("closeout-resume: %w", err)
	}
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
