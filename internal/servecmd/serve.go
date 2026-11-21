// Package servecmd implements `ard serve`: it resolves the target repo,
// takes the leader lock, opens state, builds the GitHub reader and effect
// layer, adopts running jobs into the slot pool, and runs the controller.
// Loops are registered in registerLoops, one constructor call each.
package servecmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/configsrc"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/local"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/repopath"
	"github.com/albertcbrown/ard/internal/scanner/stale"
	"github.com/albertcbrown/ard/internal/state"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

// deps is what serve needs from the outside world. Tests inject fakes
// through build, so no test builds a real GitHub client.
type deps struct {
	Reader    github.Reader
	Effects   effects.Effects
	RemoteURL string         // where the controller fetches main from
	Token     gitx.TokenFunc // mints a push and fetch credential; nil in tests
	Close     func() error   // optional
}

// builder constructs deps for repo. stateDir is where a dry-run ledger goes.
type builder func(ctx context.Context, e *cli.Env, repo github.Repo, stateDir string) (deps, error)

// Serve is the handler behind `ard serve`.
func Serve(ctx context.Context, e *cli.Env) error { return serve(ctx, e, buildDeps) }

func serve(ctx context.Context, e *cli.Env, build builder) error {
	root, err := config.RepoRoot(e.ConfigPath)
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	// The root is handed to git and to executor.Job.RepoDir, which must be
	// absolute; RepoRoot returns "." when --config is omitted.
	if root, err = filepath.Abs(root); err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	repo, err := resolveRepo(ctx, e.Repo, root)
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("ard serve: find home directory: %w", err)
	}
	if err := state.CheckLegacy(filepath.Join(home, ".ard"), repo.String()); err != nil {
		return &cli.ExitError{Code: 1, Err: fmt.Errorf("ard serve: %w", err)}
	}
	lockPath, err := controller.LockPath(home, repo.Owner, repo.Name)
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	leader, err := controller.AcquireLeader(lockPath)
	if err != nil {
		return &cli.ExitError{Code: 1, Err: fmt.Errorf("ard serve: %w", err)}
	}
	defer leader.Release()

	stateDir, err := state.Dir(filepath.Join(home, ".ard"), repo.String(), e.DryRun)
	if err != nil {
		return err
	}
	store, err := state.Open(ctx, stateDir, state.Options{})
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	defer store.Close()
	scanners := scannerdb.NewRegistry(stateDir, scannerdb.Options{})
	defer scanners.Close()

	d, err := build(ctx, e, repo, stateDir)
	if err != nil {
		return err
	}
	if d.Close != nil {
		defer d.Close()
	}

	log := e.Log
	// Config comes from origin/main, not from the working tree (#87): a merged
	// change applies on the next tick without a pull, and every job of a tick
	// runs against one commit.
	cacheDir, err := repopath.Join(filepath.Join(home, ".ard", "config"), repo.String())
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	src, err := configsrc.New(configsrc.Options{
		Git: gitx.New(""), RepoDir: root, RemoteURL: d.RemoteURL, Token: d.Token,
		CacheDir: cacheDir, Log: log,
	})
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	gate := controller.NewPauseGate(d.Reader, log)
	gate.SetLocalConfig(root)
	pausePath, err := controller.PauseFilePath(home, repo.Owner, repo.Name)
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	gate.SetPauseFile(pausePath)
	// Make `touch ~/.ard/pause/<owner>/<repo>` work on a fresh host.
	if err := os.MkdirAll(filepath.Dir(pausePath), 0o700); err != nil {
		log.Warn("serve.pause_dir_failed", "path", filepath.Dir(pausePath), "error", err)
	}

	w := &wiring{
		Log: log, Reader: d.Reader, Effects: d.Effects, Store: store, MaxPods: config.DefaultMaxPods,
		Repo: repo, Root: root, Home: home, RemoteURL: d.RemoteURL, Token: d.Token, Scanners: scanners,
	}
	defer w.closeAll()
	loader := &cfgLoader{src: src, fx: d.Effects, log: log, stderr: e.Stderr, issue: func() int { return int(w.statusIssue.Load()) }}

	// An origin/main whose config cannot be loaded does not abort startup:
	// serve starts paused, tells the status issue, and retries every tick.
	loadCtx, cancelLoad := context.WithTimeout(ctx, 2*time.Minute)
	cfg, cfgErr := loader.Load(loadCtx)
	cancelLoad()
	if cfgErr != nil {
		log.Error("serve.config_unavailable", "error", cfgErr, "hint", "starting paused; will retry every tick")
	} else {
		w.MaxPods = cfg.MaxPods
	}
	// A scan can outlast a pause, so the scanners re-check after it. The
	// check fails closed, like the gate's own.
	w.Paused = func(ctx context.Context) bool {
		c, err := loader.Load(ctx)
		if err != nil {
			return true
		}
		return gate.Check(ctx, c).Paused
	}

	cc, err := controller.New(controller.Deps{
		Log: log,
		Config: func(ctx context.Context) (*config.Resolved, error) {
			c, err := loader.Load(ctx)
			if err != nil {
				return nil, err
			}
			if p := w.pool.Load(); p != nil {
				p.SetMax(c.MaxPods) // a reload may change max_pods
			}
			return c, nil
		},
		Pause: gate,
		// Order matters: #16 needs the pool adopted before the first Offer,
		// and #20's closeout-resume (when it lands) goes first.
		Startup: []controller.Hook{
			{Name: "pool-adopt", Fn: func(ctx context.Context) error {
				var cl workspaceCleaner
				if ex, err := w.newExecutor(); err == nil {
					cl = ex
				}
				if err := failStaleScannerJobs(ctx, store, cl, log); err != nil {
					return err
				}
				p, err := adoptPool(ctx, store, w.MaxPods, log)
				if err != nil {
					return err
				}
				w.pool.Store(p)
				return nil
			}},
			{Name: "status-issue", Fn: func(ctx context.Context) error {
				id, err := d.Reader.Identity(ctx)
				if err != nil {
					return fmt.Errorf("identify github user: %w", err)
				}
				n, err := controller.EnsureStatusIssue(ctx, d.Reader, d.Effects, store, id.Login, log)
				if err != nil {
					return err
				}
				gate.SetStatusIssue(n)
				w.statusIssue.Store(int64(n))
				return nil
			}},
		},
		Force: forceOnSecondSignal(ctx),
	}, registerLoops(w)...)
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	startSHA := ""
	if cfg != nil {
		startSHA = cfg.SHA
	}
	log.Info("serve.start", "repo", repo.String(), "dry_run", e.DryRun, "pause_file", pausePath, "config_sha", startSHA)
	return cc.Run(ctx)
}

// wiring holds the long-lived dependencies loops take through their own
// constructors. The pool is not built yet when constructors run (the
// pool-adopt startup hook builds it inside Run), so loops must call Pool()
// at tick time, never in their constructors.
type wiring struct {
	Log     *slog.Logger
	Reader  github.Reader
	Effects effects.Effects
	Store   *state.Store
	MaxPods int
	Repo    github.Repo
	Root    string // absolute path of the local clone; the object store for worktrees and commits
	Home    string // the user's home directory; ~/.ard holds state, logs and worktrees
	// RemoteURL is where main is fetched from and pushed to.
	RemoteURL string
	Token     gitx.TokenFunc
	Scanners  *scannerdb.Registry
	Paused    func(ctx context.Context) bool // nil = never paused
	pool      atomic.Pointer[pool.Pool]

	statusIssue atomic.Int64 // the status issue number; 0 until the startup hook has run

	mu      sync.Mutex
	closers []func(context.Context)
}

// onClose registers fn to run when serve returns (committer worktrees, for
// example). Closers run in reverse order.
func (w *wiring) onClose(fn func(context.Context)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closers = append(w.closers, fn)
}

func (w *wiring) closeAll() {
	w.mu.Lock()
	cs := w.closers
	w.closers = nil
	w.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := len(cs) - 1; i >= 0; i-- {
		cs[i](ctx)
	}
}

// newExecutor builds the local executor every loop uses; its host-mode hook
// appends a state.EventHostUntrusted row to the controller database. Every
// loop that runs jobs (scanners, and later triage, workers and reviewers)
// must build its executor here, so host-mode warnings are recorded.
func (w *wiring) newExecutor() (*local.Local, error) {
	return local.New(local.Options{
		Root: filepath.Join(w.Home, ".ard"), Log: w.Log,
		OnHostUntrusted: recordHostUntrusted(w.Store),
	})
}

// recordHostUntrusted returns the executor hook that appends a
// state.EventHostUntrusted row for a host-mode job.
func recordHostUntrusted(store *state.Store) func(context.Context, executor.Handle) error {
	return func(ctx context.Context, h executor.Handle) error {
		return store.AppendEvent(ctx, state.Event{
			Kind: state.EventHostUntrusted, JobID: h.JobID, Issue: h.Issue, Stage: h.Stage, Role: h.Role,
		})
	}
}

// Pool returns the slot pool adopted at startup. It panics if called before
// the pool-adopt hook has run, which means a loop used it in its constructor.
func (w *wiring) Pool() *pool.Pool {
	p := w.pool.Load()
	if p == nil {
		panic("pool used before pool-adopt")
	}
	return p
}

// registerLoops returns the loops to run: the scanners loop (#23) and the
// stale closer (#25) so far. Loops that run jobs build their executor with w.newExecutor(). The triage, worker, reviewer, merger and Reaper loops each
// add one constructor call here. It is a variable so tests can observe wiring.
var registerLoops = func(w *wiring) []controller.Loop {
	var loops []controller.Loop
	if l := scannerLoop(w); l != nil {
		loops = append(loops, l)
	}
	loops = append(loops, stale.New(w.Reader, w.Effects, nil))
	return loops
}

// resolveRepo parses --repo, or falls back to the origin remote of root.
// Errors never include the remote URL, which may embed a token.
func resolveRepo(ctx context.Context, flag, root string) (github.Repo, error) {
	if flag != "" {
		r, err := github.ParseRepo(flag)
		if err != nil {
			return github.Repo{}, fmt.Errorf("ard serve: %w", err)
		}
		return r, nil
	}
	url, err := gitx.New("").RemoteURL(ctx, root, "origin")
	if err != nil {
		return github.Repo{}, errors.New("ard serve: --repo owner/repo is required (no origin remote to fall back on)")
	}
	r, err := github.RepoFromRemote(url)
	if err != nil {
		return github.Repo{}, errors.New("ard serve: --repo owner/repo is required (origin remote is not a github.com owner/repo URL)")
	}
	return r, nil
}

// forceOnSecondSignal returns a channel closed on the first signal received
// after ctx is done. cli.Run's NotifyContext consumes the first signal and
// keeps catching later ones, so without this a second Ctrl-C does nothing.
func forceOnSecondSignal(ctx context.Context) <-chan struct{} {
	force := make(chan struct{})
	go func() {
		<-ctx.Done()
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(ch)
		<-ch
		close(force)
	}()
	return force
}

// buildDeps is the production builder: credentials from the environment,
// the client and effect layer from effects.Open. Dev-token auth is refused
// outside dry-run.
func buildDeps(ctx context.Context, e *cli.Env, repo github.Repo, stateDir string) (deps, error) {
	auth, err := github.AuthFromEnv(os.Getenv, os.ReadFile)
	if err != nil {
		return deps{}, fmt.Errorf("ard serve: %w", err)
	}
	if auth.Kind() == github.AuthToken && !e.DryRun {
		return deps{}, errors.New("ard serve: ARD_GITHUB_TOKEN auth is for --dry-run only; configure a GitHub App for live runs")
	}
	red, err := redact.New(redact.FromEnviron(os.Environ()), redact.Options{})
	if err != nil {
		return deps{}, fmt.Errorf("ard serve: %w", err)
	}
	o := effects.Options{
		Redact: func(text string) string { return redactText(context.WithoutCancel(ctx), e.Log, red, text) },
		DryRun: e.DryRun,
		GitHub: github.Config{Repo: repo, Auth: auth, Log: e.Log},
		Log:    e.Log,
	}
	if e.DryRun {
		o.LedgerPath = filepath.Join(stateDir, "effects.jsonl")
	}
	fx, rd, err := effects.Open(ctx, o)
	if err != nil {
		return deps{}, fmt.Errorf("ard serve: %w", err)
	}
	d := deps{Reader: rd, Effects: fx, Token: auth.Token, RemoteURL: fmt.Sprintf("https://github.com/%s.git", repo)}
	if c, ok := fx.(interface{ Close() error }); ok {
		d.Close = c.Close
	}
	return d, nil
}

// redactText masks known secrets in text bound for the dry-run ledger and
// log. It fails closed: a scan error or hit replaces the whole text, and the
// reason is logged without the text itself.
func redactText(ctx context.Context, log *slog.Logger, rd *redact.Redactor, text string) string {
	res, err := rd.Text(ctx, []byte(text))
	switch {
	case err != nil:
		log.Warn("dryrun.redact_failed", "error", err)
		return "[REDACTED: secret scan failed]"
	case res.Flagged:
		log.Warn("dryrun.redact_flagged", "findings", len(res.Findings))
		return "[REDACTED: secret scan flagged this text]"
	}
	return string(res.Clean)
}
