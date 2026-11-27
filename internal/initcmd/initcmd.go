// Package initcmd implements `ard init`: write the starter .ard/ tree
// (internal/roles.FS) into the local checkout, or with --pr propose it as a
// PR on branch ard-init. It never writes main and never deletes a file.
// Labels are synced by `ard setup`.
package initcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/setupcmd"
)

// prTitle is the title of the PR init opens.
const prTitle = "ard init: starter .ard/ configuration"

// deps is what run needs from the outside; tests supply the GitHub fake,
// effects.NewLive(fake, gitx.New(""), log) and a file:// remote.
type deps struct {
	Repo      github.Repo
	Reader    github.Reader
	Effects   effects.Effects
	RemoteURL string
	Token     gitx.TokenFunc
	AuthKind  github.AuthKind
	Close     func() error // optional
}

// Init is the handler behind `ard init`.
func Init(ctx context.Context, e *cli.Env, o cli.InitOpts) error {
	if !o.PR {
		return runLocal(ctx, e, o, localPermissions(e))
	}
	return run(ctx, e, o, func(ctx context.Context, root string) (deps, error) {
		repo, err := resolveRepo(ctx, e.Repo, root)
		if err != nil {
			return deps{}, err
		}
		auth, err := resolveAuth(ctx, e)
		if err != nil {
			return deps{}, err
		}
		fx, rd, err := effects.Open(ctx, effects.Options{
			DryRun: e.DryRun,
			GitHub: github.Config{Repo: repo, Auth: auth, Log: e.Log},
			Log:    e.Log,
		})
		if err != nil {
			return deps{}, fmt.Errorf("ard init: %w", err)
		}
		d := deps{Repo: repo, Reader: rd, Effects: fx, Token: auth.Token, AuthKind: auth.Kind(), RemoteURL: fmt.Sprintf("https://github.com/%s.git", repo)}
		if c, ok := fx.(interface{ Close() error }); ok {
			d.Close = c.Close
		}
		return d, nil
	})
}

// localPermissions runs the permission check for local mode. Local mode
// needs no GitHub App, so anything that stops the check is only reported.
func localPermissions(e *cli.Env) permFunc {
	return func(ctx context.Context, root string) (setupcmd.Row, error) {
		repo, err := resolveRepo(ctx, e.Repo, root)
		if err != nil {
			return setupcmd.Row{}, err
		}
		auth, err := github.AuthFromEnv(os.Getenv, os.ReadFile)
		if err != nil {
			return setupcmd.Row{}, err
		}
		fx, rd, err := effects.Open(ctx, effects.Options{
			DryRun: true,
			GitHub: github.Config{Repo: repo, Auth: auth, Log: e.Log},
			Log:    e.Log,
		})
		if err != nil {
			return setupcmd.Row{}, err
		}
		if c, ok := fx.(interface{ Close() error }); ok {
			defer c.Close()
		}
		return setupcmd.CheckPermissions(ctx, rd, repo), nil
	}
}

// Seams for tests, as in servecmd.
var (
	lookPath = exec.LookPath
	runGh    = github.ExecRun
)

// resolveAuth reads credentials from the environment and, when none are set,
// falls back to the gh CLI's token. The token stays in memory.
func resolveAuth(ctx context.Context, e *cli.Env) (github.Auth, error) {
	auth, err := github.AuthFromEnv(os.Getenv, os.ReadFile)
	if errors.Is(err, github.ErrNoCredentials) {
		ta, gerr := github.GhFallback(ctx, lookPath, runGh, e.Stderr)
		if gerr != nil {
			return nil, fmt.Errorf("ard init: %w", gerr)
		}
		return ta, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ard init: %w", err)
	}
	return auth, nil
}

// resolveRepo takes --repo, or falls back to the origin remote of root. Its
// errors never include the remote URL, which may embed a token.
func resolveRepo(ctx context.Context, flag, root string) (github.Repo, error) {
	if flag != "" {
		r, err := github.ParseRepo(flag)
		if err != nil {
			return github.Repo{}, fmt.Errorf("ard init: %w", err)
		}
		return r, nil
	}
	url, err := gitx.New("").RemoteURL(ctx, root, "origin")
	if err != nil {
		return github.Repo{}, errors.New("ard init: --repo owner/repo is required (no origin remote to fall back on)")
	}
	r, err := github.RepoFromRemote(url)
	if err != nil {
		return github.Repo{}, errors.New("ard init: --repo owner/repo is required (origin remote is not a github.com owner/repo URL)")
	}
	return r, nil
}

// run does every check before any write; see the order in the comments.
func run(ctx context.Context, e *cli.Env, o cli.InitOpts, open func(ctx context.Context, root string) (deps, error)) error {
	// 1. The checkout is only the object store and the origin fallback.
	root, err := config.RepoRoot(e.ConfigPath)
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	if root, err = filepath.Abs(root); err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return fmt.Errorf("ard init: %s is not the root of a git repository (run it there, or pass --config <repo>/.ard/config.yaml)", root)
	}
	// 2-3. Repo and credentials.
	d, err := open(ctx, root)
	if err != nil {
		return err
	}
	if d.Close != nil {
		defer d.Close()
	}
	// 3b. The shared permission check: in --pr mode a failure stops the run.
	if row := setupcmd.CheckPermissions(ctx, d.Reader, d.Repo); row.Status != setupcmd.StatusOK {
		return fmt.Errorf("ard init: permissions: %s", row.Detail)
	}
	// 4. Who commits.
	id, err := d.Reader.Identity(ctx)
	if err != nil {
		return fmt.Errorf("ard init: identify github user: %w", err)
	}

	tmp, err := os.MkdirTemp("", "ard-init-")
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	defer os.RemoveAll(tmp)

	// 5. Fetch main; refuse while ard-init exists.
	in, err := committer.NewInit(ctx, committer.Config{
		RepoDir: root, WorkDir: filepath.Join(tmp, "work"), RemoteURL: d.RemoteURL,
		Bot: id, Token: d.Token, Log: e.Log,
	}, d.Effects, nil)
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	base, err := in.Base(ctx)
	switch {
	case errors.Is(err, committer.ErrInitExists):
		return initExistsErr(ctx, d)
	case errors.Is(err, committer.ErrBranchGone):
		return fmt.Errorf("ard init: %s has no main branch; push an initial commit first", d.Repo)
	case err != nil:
		return fmt.Errorf("ard init: fetch main: %w", err)
	}

	// 6. Plan against main, then validate the result.
	files, err := starter()
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	snap := filepath.Join(tmp, "main")
	if err := gitx.New("").ExportPaths(ctx, root, base, snap, append(append(config.SourcePaths(), ContextPaths()...), LockPath)...); err != nil {
		return fmt.Errorf("ard init: read main's .ard/: %w", err)
	}
	existing := os.DirFS(snap)
	var lock Lock
	if data, err := os.ReadFile(filepath.Join(snap, filepath.FromSlash(LockPath))); err == nil {
		var warns []string
		lock, warns = ParseLock(data)
		for _, w := range warns {
			fmt.Fprintln(e.Stderr, "warning: "+w)
		}
	}
	pl, err := plan(existing, files, lock)
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	ops := pl.Ops
	toWrite := changed(ops)
	if len(toWrite) > 0 {
		data, err := withLock(lock, ops, o.Version).Marshal()
		if err != nil {
			return fmt.Errorf("ard init: %w", err)
		}
		toWrite = append(toWrite, committer.InitFile{Path: LockPath, Data: data})
	}
	merged, err := overlay(existing, toWrite)
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	res, err := config.LoadFS(merged, root)
	if err != nil {
		return fmt.Errorf("ard init: the resulting configuration is invalid, nothing was written:\n%w", err)
	}

	// 7. Propose.
	var pr github.PullRequest
	if len(toWrite) > 0 {
		pres, err := in.Propose(ctx, base, toWrite)
		switch {
		case errors.Is(err, committer.ErrInitExists):
			return initExistsErr(ctx, d)
		case err != nil:
			return fmt.Errorf("ard init: %w", err)
		case pres.NoOp:
			toWrite = nil
		}
	}
	if len(toWrite) == 0 {
		if len(toWrite) == 0 {
			printLeftAlone(e, pl)
		}
		fmt.Fprintln(e.Stdout, "nothing to propose: main already has the starter files, or only edited ones differ")
		e.Log.InfoContext(ctx, "init.done", "repo", d.Repo.String(), "base", base, "files", 0, "pr", 0, "dry_run", e.DryRun)
		return nil
	}
	printOps(e, ops)
	printLeftAlone(e, pl)

	// 8. Open the PR.
	pr, err = d.Effects.CreatePR(ctx, github.NewPR{
		Title: prTitle, Body: initBody(pl, res, d.AuthKind), Head: committer.InitBranch, Base: "main",
	})
	if err != nil {
		return fmt.Errorf("ard init: branch %s pushed but opening the PR failed: %w; open it by hand, or delete the branch and rerun", committer.InitBranch, err)
	}

	// 9. Report.
	if e.DryRun {
		fmt.Fprintf(e.Stdout, "dry-run: would open a PR from %s\n", committer.InitBranch)
	} else {
		fmt.Fprintf(e.Stdout, "opened PR #%d: %s\n", pr.Number, pr.URL)
	}
	fmt.Fprintln(e.Stdout, "next: merge the PR, then run `ard setup`")
	e.Log.InfoContext(ctx, "init.done", "repo", d.Repo.String(), "base", base, "files", len(toWrite), "pr", pr.Number, "dry_run", e.DryRun)
	return nil
}

// printLeftAlone prints the files init did not touch: edited ones with their
// diff against the starter, and ones the starter dropped.
func printLeftAlone(e *cli.Env, pl Result) {
	for _, o := range pl.Ops {
		if o.Act == actEdited {
			fmt.Fprintf(e.Stdout, "edited %s: left alone; the starter differs:\n%s", o.File.Path, o.Diff)
		}
	}
	for _, p := range pl.Dropped {
		fmt.Fprintf(e.Stdout, "%s: no longer in the starter, left in place\n", p)
	}
}

// printOps writes one line per file the PR adds or updates.
func printOps(e *cli.Env, ops []op) {
	for _, o := range ops {
		if o.Act == actUnchanged || o.Act == actEdited {
			continue
		}
		verb := string(o.Act)
		if e.DryRun {
			verb = "would " + verb
		} else {
			verb += " "
		}
		mode := ""
		if o.File.Exec {
			mode = " (0755)"
		}
		fmt.Fprintf(e.Stdout, "%s %s%s\n", verb, o.File.Path, mode)
	}
}

// initExistsErr is the refusal when ard-init is already on the remote. It
// names the open PR when there is one.
func initExistsErr(ctx context.Context, d deps) error {
	msg := "ard init: branch ard-init already exists on the remote"
	if prs, err := d.Reader.ListPRs(ctx, github.ListPRsOpts{Head: committer.InitBranch, State: github.StateOpen}); err == nil && len(prs) > 0 {
		msg += fmt.Sprintf(" (open PR #%d)", prs[0].Number)
	}
	return errors.New(strings.TrimSpace(msg) + "; merge or close it and delete the branch, then rerun")
}
