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
	"sync/atomic"
	"syscall"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

// deps is what serve needs from the outside world. Tests inject fakes
// through build, so no test builds a real GitHub client.
type deps struct {
	Reader  github.Reader
	Effects effects.Effects
	Close   func() error // optional
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
	repo, err := resolveRepo(e.Repo)
	if err != nil {
		return err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("ard serve: find home directory: %w", err)
	}
	leader, err := controller.AcquireLeader(controller.LockPath(home, repo.Owner, repo.Name))
	if err != nil {
		return &cli.ExitError{Code: 1, Err: fmt.Errorf("ard serve: %w", err)}
	}
	defer leader.Release()

	stateDir, err := state.Dir(filepath.Join(home, ".ard"), repo.Owner+"/"+repo.Name, e.DryRun)
	if err != nil {
		return err
	}
	store, err := state.Open(ctx, stateDir, state.Options{})
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	defer store.Close()

	d, err := build(ctx, e, repo, stateDir)
	if err != nil {
		return err
	}
	if d.Close != nil {
		defer d.Close()
	}

	log := e.Log
	gate := controller.NewPauseGate(d.Reader, log)
	w := &wiring{Log: log, Reader: d.Reader, Effects: d.Effects, Store: store, MaxPods: cfg.MaxPods}
	var poolRef atomic.Pointer[pool.Pool]

	cc, err := controller.New(controller.Deps{
		Log: log,
		Config: func() (*config.Resolved, error) {
			c, err := config.Load(root)
			if err != nil {
				return nil, err
			}
			if p := poolRef.Load(); p != nil {
				p.SetMax(c.MaxPods) // a reload may change max_pods
			}
			return c, nil
		},
		Pause: gate,
		// Order matters: #16 needs the pool adopted before the first Offer,
		// and #20's closeout-resume (when it lands) goes first.
		Startup: []controller.Hook{
			{Name: "pool-adopt", Fn: func(ctx context.Context) error {
				p, err := adoptPool(ctx, store, cfg.MaxPods, log)
				if err != nil {
					return err
				}
				w.Pool = p
				poolRef.Store(p)
				return nil
			}},
			{Name: "status-issue", Fn: func(ctx context.Context) error {
				id, err := d.Reader.Identity(ctx)
				if err != nil {
					return fmt.Errorf("identify github user: %w", err)
				}
				n, err := controller.EnsureStatusIssue(ctx, d.Reader, d.Effects, id.Login, log)
				if err != nil {
					return err
				}
				gate.SetStatusIssue(n)
				return nil
			}},
		},
		Force: forceOnSecondSignal(ctx),
	}, registerLoops(w)...)
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	log.Info("serve.start", "repo", repo.Owner+"/"+repo.Name, "dry_run", e.DryRun)
	return cc.Run(ctx)
}

// wiring holds the long-lived dependencies loops take through their own
// constructors. Pool is set by the pool-adopt startup hook, which runs
// before any loop ticks.
type wiring struct {
	Log     *slog.Logger
	Reader  github.Reader
	Effects effects.Effects
	Store   *state.Store
	Pool    *pool.Pool
	MaxPods int
}

// registerLoops returns the loops to run. None yet: #23 (scanners), #25
// (stale) and the triage, worker, reviewer, merger and Reaper loops each add
// one constructor call here.
func registerLoops(*wiring) []controller.Loop { return nil }

// resolveRepo parses --repo, which is required.
// TODO: fall back to origin via gitx.RemoteURL once #22 lands.
func resolveRepo(flag string) (github.Repo, error) {
	if flag == "" {
		return github.Repo{}, errors.New("ard serve: --repo owner/repo is required")
	}
	r, err := github.ParseRepo(flag)
	if err != nil {
		return github.Repo{}, fmt.Errorf("ard serve: %w", err)
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
	o := effects.Options{
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
	d := deps{Reader: rd, Effects: fx}
	if c, ok := fx.(interface{ Close() error }); ok {
		d.Close = c.Close
	}
	return d, nil
}
