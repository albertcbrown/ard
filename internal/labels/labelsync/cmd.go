package labelsync

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/labels"
)

// deps is what run needs from the outside world.
type deps struct {
	Reader Reader
	Writer Writer
}

// Command is the handler behind `ard labels sync`.
func Command(ctx context.Context, e *cli.Env) error {
	return run(ctx, e, func(ctx context.Context, root string) (deps, error) {
		repo, err := resolveRepo(ctx, e, root)
		if err != nil {
			return deps{}, err
		}
		auth, err := github.AuthFromEnv(os.Getenv, os.ReadFile)
		if err != nil {
			return deps{}, fmt.Errorf("labels sync: %w", err)
		}
		fx, rd, err := effects.Open(ctx, effects.Options{
			DryRun: e.DryRun,
			GitHub: github.Config{Repo: repo, Auth: auth, Log: e.Log},
			Log:    e.Log,
		})
		if err != nil {
			return deps{}, fmt.Errorf("labels sync: %w", err)
		}
		return deps{Reader: rd, Writer: fx}, nil
	})
}

// run loads and validates the configuration before it builds any GitHub
// client, so a bad config never reaches the network.
func run(ctx context.Context, e *cli.Env, open func(ctx context.Context, root string) (deps, error)) error {
	root, err := repoRoot(e.ConfigPath)
	if err != nil {
		return err
	}
	res, err := config.Load(root)
	if err != nil {
		return fmt.Errorf("labels sync: %w", err)
	}
	want, err := labels.Desired(labels.SpecFrom(res))
	if err != nil {
		return err
	}
	d, err := open(ctx, root)
	if err != nil {
		return err
	}
	rep, err := Sync(ctx, d.Reader, d.Writer, want, e.Log)
	Print(e.Stdout, rep, e.DryRun)
	return err
}

// resolveRepo takes --repo, or falls back to the origin remote of the
// repository that holds the config. config.Resolved carries no owner/name.
func resolveRepo(ctx context.Context, e *cli.Env, root string) (github.Repo, error) {
	if e.Repo != "" {
		return github.ParseRepo(e.Repo)
	}
	url, err := gitx.New("").RemoteURL(ctx, root, "origin")
	if err != nil {
		return github.Repo{}, fmt.Errorf("labels sync: --repo owner/repo is required (no origin remote to fall back on: %w)", err)
	}
	repo, err := github.RepoFromRemote(url)
	if err != nil {
		return github.Repo{}, fmt.Errorf("labels sync: --repo owner/repo is required (cannot use the origin remote: %w)", err)
	}
	e.Log.Info("repo taken from the origin remote", "repo", repo.String())
	return repo, nil
}

// repoRoot maps --config to the repository root config.Load expects: the
// default is the current directory; a path inside a .ard directory strips it.
// TODO: the same helper is unexported in configcmd; export it as
// config.RepoRoot and use it from both.
func repoRoot(configPath string) (string, error) {
	if configPath == "" {
		return ".", nil
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return "", fmt.Errorf("labels sync: resolving --config: %w", err)
	}
	dir := filepath.Dir(abs)
	if filepath.Base(dir) == ".ard" {
		return filepath.Dir(dir), nil
	}
	return dir, nil
}

// Print writes one line per change, stale label and failure, then a summary.
// Unchanged labels get no line.
func Print(w io.Writer, rep Report, dryRun bool) {
	create, update := "create", "update"
	if dryRun {
		create, update = "would create", "would update"
	}
	line := func(verb, rest string) {
		pad := 8 - len(verb)
		if pad < 1 {
			pad = 1
		}
		fmt.Fprintf(w, "%s%s%s\n", verb, strings.Repeat(" ", pad), rest)
	}
	failedCreates := 0
	for _, l := range rep.Create {
		line(create, l.Name)
		if _, bad := rep.Failed[l.Name]; bad {
			failedCreates++
		}
	}
	failedUpdates := 0
	for _, u := range rep.Update {
		line(update, fmt.Sprintf("%s (%s)", u.Want.Name, diffSummary(u)))
		if _, bad := rep.Failed[u.Want.Name]; bad {
			failedUpdates++
		}
	}
	for _, s := range rep.Stale {
		line("stale", s)
	}
	names := make([]string, 0, len(rep.Failed))
	for n := range rep.Failed {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		line("failed", fmt.Sprintf("%s: %v", n, rep.Failed[n]))
	}
	created, updated := len(rep.Create)-failedCreates, len(rep.Update)-failedUpdates
	if dryRun {
		fmt.Fprintf(w, "dry-run: would create %d, would update %d, unchanged %d, stale %d\n",
			len(rep.Create), len(rep.Update), len(rep.Unchanged), len(rep.Stale))
		return
	}
	fmt.Fprintf(w, "created %d, updated %d, unchanged %d, stale %d\n", created, updated, len(rep.Unchanged), len(rep.Stale))
}

func diffSummary(u Update) string {
	var parts []string
	if !strings.EqualFold(strings.TrimPrefix(u.Have.Color, "#"), u.Want.Color) {
		parts = append(parts, fmt.Sprintf("color %s -> %s", u.Have.Color, u.Want.Color))
	}
	if u.Have.Description != u.Want.Description {
		parts = append(parts, "description changed")
	}
	return strings.Join(parts, ", ")
}
