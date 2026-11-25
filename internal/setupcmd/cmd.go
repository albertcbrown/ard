package setupcmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/labels/labelsync"
)

// Command is the handler behind `ard setup`. The config is loaded and
// validated before any GitHub client exists.
func Command(ctx context.Context, e *cli.Env, o cli.SetupOpts) error {
	root, err := config.RepoRoot(e.ConfigPath)
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	res, err := config.Load(root)
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	want, err := labels.Desired(labels.SpecFrom(res))
	if err != nil {
		return err
	}
	repo, err := labelsync.ResolveRepo(ctx, e, root)
	if err != nil {
		return err
	}
	auth, err := github.AuthFromEnv(os.Getenv, os.ReadFile)
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	dry := e.DryRun && !o.Check
	fx, rd, err := effects.Open(ctx, effects.Options{
		DryRun: dry,
		GitHub: github.Config{Repo: repo, Auth: auth, Log: e.Log},
		Log:    e.Log,
	})
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	d := Deps{
		Reader:      rd,
		Policy:      rd,
		Repo:        repo,
		Writer:      fx,
		Desired:     want,
		Permissions: func(ctx context.Context) Row { return CheckPermissions(ctx, rd, repo) },
		LocalVsMain: func(ctx context.Context) (string, error) { return LocalVsMain(ctx, root) },
		DryRun:      dry,
		Log:         e.Log,
	}
	return execute(ctx, e, d, o)
}

// execute runs setup with the given dependencies and maps the report to
// the command's outcome: exit 2 on drift under --check, an error when
// manual steps are needed.
func execute(ctx context.Context, e *cli.Env, d Deps, o cli.SetupOpts) error {
	rep, err := Run(ctx, d, Opts{Check: o.Check}, e.Stdout)
	Print(e.Stdout, rep, d.DryRun)
	if err != nil {
		return err
	}
	if o.Check && rep.HasDrift() {
		return &cli.ExitError{Code: 2, Err: errors.New("setup: drift")}
	}
	if !rep.Green() {
		return errors.New("setup: manual steps needed")
	}
	return nil
}

// LocalVsMain returns a warning when ./.ard/ in the worktree differs from
// origin/main, which is what ard serve reads. It never fails the run.
func LocalVsMain(ctx context.Context, root string) (string, error) {
	g := gitx.New("")
	if _, err := g.RevParse(ctx, root, "origin/main"); err != nil {
		return "origin/main not found; cannot compare ./.ard/ with what ard serve reads", nil
	}
	ch, err := g.ChangedSince(ctx, root, root, "origin/main", ".ard/")
	if err != nil {
		return "", err
	}
	if len(ch) > 0 {
		return "./.ard/ differs from origin/main (ard serve reads origin/main)", nil
	}
	return "", nil
}

// CheckPermissions is the permission check shared by `ard setup` and
// `ard init`. Until the full check (#302) lands it only verifies that the
// repository is readable with the configured credentials.
func CheckPermissions(ctx context.Context, r labelsync.Reader, repo github.Repo) Row {
	if _, err := r.ListLabels(ctx); err != nil {
		return Row{Status: StatusManual, Detail: "check ARD_GITHUB_* credentials and that the App is installed on " + repo.String()}
	}
	return Row{Status: StatusOK, Detail: "repository reachable"}
}
