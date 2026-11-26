package setupcmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

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
	if o.PrintWorkflow {
		// Nothing else may reach stdout, so a redirect to a file is clean.
		return PrintWorkflow(e.Stdout)
	}
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
	if o.CreateGHApp {
		if err := CreateApp(ctx, e, repo, CreateAppOpts{Name: o.AppName, KeyPath: o.KeyFile, Force: o.Force}); err != nil {
			return err
		}
		if e.DryRun {
			return nil
		}
	}
	auth, err := github.AuthFromEnv(os.Getenv, os.ReadFile)
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	dry := e.DryRun && !o.Check
	fx, rd, err := effects.Open(ctx, effects.Options{
		DryRun:        dry,
		GitHub:        github.Config{Repo: repo, Auth: auth, Log: e.Log},
		OperatorToken: operatorToken(auth, e),
		Log:           e.Log,
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
		Variables:   fx,
		Confirm:     confirm(os.Stdin, os.Stderr),
		WorkflowAt: func(ctx context.Context) ([]byte, bool, error) {
			g := gitx.New("")
			if _, err := g.RevParse(ctx, root, "origin/main"); err != nil {
				return nil, false, errors.New("origin/main not found")
			}
			return g.FileAt(ctx, root, "origin/main", workflowPath, 1<<20)
		},
		DryRun: dry,
		Log:    e.Log,
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
		return Row{Status: StatusManual, Detail: "check ARD_GITHUB_* credentials and that the App is installed on " + repo.String() + " with the permissions in AppPermissions (never " + strings.Join(ForbiddenPermissions, ", ") + ")"}
	}
	return Row{Status: StatusOK, Detail: "repository reachable"}
}

// operatorToken returns the func that yields the operator's own token: the
// PAT from ARD_GITHUB_TOKEN when that is the configured auth, otherwise the
// gh CLI's token. It is read only when a variable is written, held in memory
// and never logged.
func operatorToken(auth github.Auth, e *cli.Env) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if t, ok := auth.(*github.TokenAuth); ok {
			return t.Value, nil
		}
		ta, err := github.GhFallback(ctx, exec.LookPath, github.ExecRun, e.Stderr)
		if err != nil {
			return "", err
		}
		return ta.Value, nil
	}
}

// confirm asks a yes/no question on a terminal. Without a terminal it
// answers no, so a run never blocks or overwrites silently.
func confirm(in *os.File, out io.Writer) func(string) (bool, error) {
	return func(prompt string) (bool, error) {
		fi, err := in.Stat()
		if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
			return false, nil
		}
		fmt.Fprintf(out, "%s [y/N] ", prompt)
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && line == "" {
			return false, err
		}
		a := strings.ToLower(strings.TrimSpace(line))
		return a == "y" || a == "yes", nil
	}
}
