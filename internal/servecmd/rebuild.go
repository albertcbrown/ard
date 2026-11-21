package servecmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/scanner"
	"github.com/albertcbrown/ard/internal/state"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

// readerBuilder returns a read-only GitHub view for repo.
type readerBuilder func(ctx context.Context, e *cli.Env, repo github.Repo) (github.Reader, func() error, error)

// RebuildState is the handler behind `ard rebuild-state <scanner>`.
func RebuildState(ctx context.Context, e *cli.Env, name string) error {
	return rebuildState(ctx, e, name, buildReader)
}

// buildReader opens a read-only client: rebuild never writes to GitHub, so it
// asks for the dry-run client even when --dry-run is not set.
func buildReader(ctx context.Context, e *cli.Env, repo github.Repo) (github.Reader, func() error, error) {
	auth, err := github.AuthFromEnv(os.Getenv, os.ReadFile)
	if err != nil {
		return nil, nil, fmt.Errorf("ard rebuild-state: %w", err)
	}
	_, rd, err := effects.Open(ctx, effects.Options{DryRun: true, GitHub: github.Config{Repo: repo, Auth: auth, Log: e.Log}, Log: e.Log})
	if err != nil {
		return nil, nil, fmt.Errorf("ard rebuild-state: %w", err)
	}
	return rd, nil, nil
}

func rebuildState(ctx context.Context, e *cli.Env, name string, build readerBuilder) error {
	root, err := config.RepoRoot(e.ConfigPath)
	if err != nil {
		return fmt.Errorf("ard rebuild-state: %w", err)
	}
	repo, err := resolveRepo(ctx, e.Repo, root)
	if err != nil {
		return err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	role, ok := cfg.Role(config.KindScanner, name)
	if !ok || role.ScanPath == "" {
		return fmt.Errorf("ard rebuild-state: %q is not a scanner with a scan executable (built-in and LLM scanners keep no rebuildable state)", name)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("ard rebuild-state: find home directory: %w", err)
	}
	// A running controller would write from a view this rebuild is replacing.
	if err := state.CheckLegacy(filepath.Join(home, ".ard"), repo.String()); err != nil {
		return &cli.ExitError{Code: 1, Err: fmt.Errorf("ard rebuild-state: %w", err)}
	}
	lockPath, err := controller.LockPath(home, repo.Owner, repo.Name)
	if err != nil {
		return fmt.Errorf("ard rebuild-state: %w", err)
	}
	leader, err := controller.AcquireLeader(lockPath)
	if err != nil {
		if errors.Is(err, controller.ErrLeaderHeld) {
			return &cli.ExitError{Code: 1, Err: fmt.Errorf("ard rebuild-state: stop ard serve first: %w", err)}
		}
		return fmt.Errorf("ard rebuild-state: %w", err)
	}
	defer leader.Release()

	// The live state only: the dry-run database holds synthetic issues that
	// are not on GitHub, so it is never rebuilt.
	stateDir, err := state.Dir(filepath.Join(home, ".ard"), repo.String(), false)
	if err != nil {
		return err
	}
	dbPath, err := scannerdb.Path(stateDir, name)
	if err != nil {
		return err
	}
	_, statErr := os.Stat(dbPath)
	haveDB := statErr == nil
	if e.DryRun && !haveDB {
		e.Log.Info("rebuild-state.no_database", "scanner", name)
	}

	rd, closeFn, err := build(ctx, e, repo)
	if err != nil {
		return err
	}
	if closeFn != nil {
		defer closeFn()
	}
	fresh, err := scanner.Rebuild(ctx, rd, cfg, name, time.Now())
	if err != nil {
		return fmt.Errorf("ard rebuild-state: %w", err)
	}

	old := scanner.NewSnapshot(nil, nil)
	var db *scannerdb.DB
	if haveDB || !e.DryRun {
		if err := state.EnsureDir(stateDir); err != nil {
			return err
		}
		if db, err = scannerdb.Open(ctx, stateDir, name, scannerdb.Options{}); err != nil {
			return fmt.Errorf("ard rebuild-state: %w", err)
		}
		defer db.Close()
		rows, err := db.Findings(ctx)
		if err != nil {
			return fmt.Errorf("ard rebuild-state: %w", err)
		}
		sups, err := db.Suppressions(ctx)
		if err != nil {
			return fmt.Errorf("ard rebuild-state: %w", err)
		}
		old = scanner.NewSnapshot(rows, sups)
	}

	rep := scanner.DiffSnapshots(old, fresh)
	printReport(e, name, rep, fresh, e.DryRun)
	if e.DryRun || db == nil {
		return nil
	}
	rows, sups := fresh.Sorted()
	if err := db.Replace(ctx, rows, sups); err != nil {
		return fmt.Errorf("ard rebuild-state: %w", err)
	}
	fmt.Fprintf(e.Stdout, "replaced state of scanner %s: %d findings, %d suppressions\n", name, len(rows), len(sups))
	return nil
}

func printReport(e *cli.Env, name string, rep scanner.RebuildReport, fresh scanner.Snapshot, dry bool) {
	mode := ""
	if dry {
		mode = " (dry-run: nothing written)"
	}
	if rep.Empty() {
		fmt.Fprintf(e.Stdout, "scanner %s: the database already matches GitHub (%d findings, %d suppressions)%s\n",
			name, len(fresh.Rows), len(fresh.Suppressions), mode)
		return
	}
	fmt.Fprintf(e.Stdout, "scanner %s: %d added, %d removed, %d changed%s\n", name, len(rep.Added), len(rep.Removed), len(rep.Changed), mode)
	for _, l := range rep.Added {
		fmt.Fprintln(e.Stdout, "  + "+l)
	}
	for _, l := range rep.Removed {
		fmt.Fprintln(e.Stdout, "  - "+l)
	}
	for _, l := range rep.Changed {
		fmt.Fprintln(e.Stdout, "  ~ "+l)
	}
}
