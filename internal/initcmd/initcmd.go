// Package initcmd implements `ard init`: propose the starter .ard/ tree
// (internal/roles.FS) as a PR on branch ard-init, and sync the label
// catalogue. It never writes the working tree or main.
package initcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/labels/labelsync"
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
	Close     func() error // optional
}

// Init is the handler behind `ard init`.
func Init(ctx context.Context, e *cli.Env, o cli.InitOpts) error {
	return run(ctx, e, o, func(ctx context.Context, root string) (deps, error) {
		repo, err := resolveRepo(ctx, e.Repo, root)
		if err != nil {
			return deps{}, err
		}
		auth, err := github.AuthFromEnv(os.Getenv, os.ReadFile)
		if err != nil {
			return deps{}, fmt.Errorf("ard init: %w", err)
		}
		if err := checkAuth(auth.Kind(), e.DryRun); err != nil {
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
		d := deps{Repo: repo, Reader: rd, Effects: fx, Token: auth.Token, RemoteURL: fmt.Sprintf("https://github.com/%s.git", repo)}
		if c, ok := fx.(interface{ Close() error }); ok {
			d.Close = c.Close
		}
		return d, nil
	})
}

// checkAuth refuses a personal token for a live run, as serve does.
func checkAuth(kind github.AuthKind, dryRun bool) error {
	if kind == github.AuthToken && !dryRun {
		return errors.New("ard init: ARD_GITHUB_TOKEN auth is for --dry-run only; configure a GitHub App")
	}
	return nil
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
	if err := gitx.New("").ExportPaths(ctx, root, base, snap, append(config.SourcePaths(), ContextPaths()...)...); err != nil {
		return fmt.Errorf("ard init: read main's .ard/: %w", err)
	}
	existing := os.DirFS(snap)
	ops, err := plan(existing, files, o.Force)
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	merged, err := overlay(existing, changed(ops))
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	res, err := config.LoadFS(merged, root)
	if err != nil {
		return fmt.Errorf("ard init: the resulting configuration is invalid, nothing was written:\n%w", err)
	}
	want, err := labels.Desired(labels.SpecFrom(res))
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}

	// 7. Labels first: a failure leaves nothing pushed, so a rerun is clean.
	rep, err := labelsync.Sync(ctx, d.Reader, d.Effects, want, e.Log)
	if err != nil && rep.Failed == nil {
		return fmt.Errorf("ard init: %w", err)
	}
	labelsync.Print(e.Stdout, rep, e.DryRun)
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}

	// 8. Propose.
	toWrite := changed(ops)
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
		fmt.Fprintln(e.Stdout, "nothing to propose: main already has the starter files")
		e.Log.InfoContext(ctx, "init.done", "repo", d.Repo.String(), "base", base, "files", 0, "pr", 0, "dry_run", e.DryRun)
		return nil
	}
	printOps(e, ops)

	// 9. Open the PR.
	pr, err = d.Effects.CreatePR(ctx, github.NewPR{
		Title: prTitle, Body: initBody(ops, res), Head: committer.InitBranch, Base: "main",
	})
	if err != nil {
		return fmt.Errorf("ard init: branch %s pushed but opening the PR failed: %w; open it by hand, or delete the branch and rerun", committer.InitBranch, err)
	}

	// 10. Report.
	if e.DryRun {
		fmt.Fprintf(e.Stdout, "dry-run: would open a PR from %s\n", committer.InitBranch)
	} else {
		fmt.Fprintf(e.Stdout, "opened PR #%d: %s\n", pr.Number, pr.URL)
	}
	e.Log.InfoContext(ctx, "init.done", "repo", d.Repo.String(), "base", base, "files", len(toWrite), "pr", pr.Number, "dry_run", e.DryRun)
	return nil
}

// printOps writes one line per file the PR adds or overwrites.
func printOps(e *cli.Env, ops []op) {
	for _, o := range ops {
		if o.Act == actUnchanged {
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
