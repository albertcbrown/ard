package servecmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/repopath"
)

const (
	seedRef     = "refs/ard/seed/main" // set by the first successful seed fetch
	seedTimeout = 30 * time.Minute
)

// cloneGit is the slice of gitx the controller clone needs. *gitx.Git
// satisfies it; it has no Commit or Push.
type cloneGit interface {
	InitBare(ctx context.Context, dir string) error
	IsBareRepo(ctx context.Context, dir string) (bool, error)
	SetConfig(ctx context.Context, gitDir, key, value string) error
	FetchRef(ctx context.Context, repoDir, url, refspec string, token gitx.TokenFunc) error
	RevParse(ctx context.Context, dir, ref string) (string, error)
}

// clonePath is <home>/.ard/repos/<owner>/<repo>.git, lowercased.
func clonePath(home string, r github.Repo) (string, error) {
	p, err := repopath.Join(filepath.Join(home, ".ard", "repos"), r.String())
	if err != nil {
		return "", err
	}
	return p + ".git", nil
}

// laneWorkDir is <home>/.ard/work/<owner>/<repo>/lanes: committer lane worktrees.
func laneWorkDir(home string, r github.Repo) (string, error) {
	d, err := repoWorkDir(home, r)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "lanes"), nil
}

// gcOff are the repo-local settings that keep git from detaching a gc that
// would run worktree prune outside the worktree lock.
var gcOff = [][2]string{{"gc.auto", "0"}, {"maintenance.auto", "false"}}

func setGCOff(ctx context.Context, g cloneGit, dir string) error {
	for _, kv := range gcOff {
		if err := g.SetConfig(ctx, dir, kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// openClone makes sure the controller's bare clone exists, is ard's, has
// auto-gc off and has been seeded, and returns its path. It also removes
// committer lane directories left by the checkout-based layout.
func openClone(ctx context.Context, g cloneGit, home string, r github.Repo, remoteURL string, token gitx.TokenFunc, log *slog.Logger) (string, error) {
	p, err := clonePath(home, r)
	if err != nil {
		return "", fmt.Errorf("controller clone path: %w", err)
	}
	tmp := p + ".tmp-init"
	if err := os.RemoveAll(tmp); err != nil {
		return "", fmt.Errorf("clear %s: %w", tmp, err)
	}
	fi, err := os.Lstat(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := createClone(ctx, g, p, tmp); err != nil {
			os.RemoveAll(tmp)
			return "", fmt.Errorf("create controller clone %s: %w", p, err)
		}
		log.Info("serve.clone_created", "path", p)
	case err != nil:
		return "", fmt.Errorf("stat controller clone %s: %w", p, err)
	case !fi.IsDir():
		return "", fmt.Errorf("%s is not a directory ard created; stop ard serve, remove it and restart", p)
	default:
		bare, berr := g.IsBareRepo(ctx, p)
		if berr != nil || !bare {
			why := "not bare"
			if berr != nil {
				why = berr.Error()
			}
			return "", fmt.Errorf("%s is not a bare git repository (%s); stop ard serve, remove it and restart", p, why)
		}
		if err := setGCOff(ctx, g, p); err != nil {
			return "", fmt.Errorf("controller clone %s: %w", p, err)
		}
	}

	if _, err := g.RevParse(ctx, p, seedRef); err != nil {
		seedClone(ctx, g, p, remoteURL, token, log)
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("seed controller clone: %w", err)
		}
	}
	removeLegacyLanes(home, r, log)
	return p, nil
}

// createClone builds the repo at tmp and renames it into place, so a crash
// never leaves a half-made repo at p.
func createClone(ctx context.Context, g cloneGit, p, tmp string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if err := g.InitBare(ctx, tmp); err != nil {
		return err
	}
	if err := setGCOff(ctx, g, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// seedClone fetches main once with a long bound: the first full fetch would
// otherwise run under the config load's 2 minute limit and could never finish
// on a large repo. A failure is logged, not returned: serve starts paused (the
// config load fails the same way) and the next start retries.
func seedClone(ctx context.Context, g cloneGit, p, remoteURL string, token gitx.TokenFunc, log *slog.Logger) {
	start := time.Now()
	sctx, cancel := context.WithTimeout(ctx, seedTimeout)
	defer cancel()
	if err := g.FetchRef(sctx, p, remoteURL, "+refs/heads/main:"+seedRef, token); err != nil {
		if ctx.Err() == nil {
			log.Error("serve.clone_seed_failed", "error", err,
				"hint", "config loads keep fetching; the next start retries the seed")
		}
		return
	}
	sha, _ := g.RevParse(sctx, p, seedRef)
	log.Info("serve.clone_seeded", "sha", sha, "elapsed", time.Since(start).Round(time.Millisecond))
}

// removeLegacyLanes deletes the committer-* directories the checkout-based
// layout kept directly under ~/.ard/work/<owner>/<repo>. No git runs: their
// registrations in the operator's checkout dangle until the operator prunes.
func removeLegacyLanes(home string, r github.Repo, log *slog.Logger) {
	dir, err := repoWorkDir(home, r)
	if err != nil {
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, en := range ents {
		if !strings.HasPrefix(en.Name(), "committer-") {
			continue
		}
		path := filepath.Join(dir, en.Name())
		if err := os.RemoveAll(path); err != nil {
			log.Warn("serve.legacy_lane_remove_failed", "path", path, "error", err)
			continue
		}
		log.Info("serve.legacy_lane_removed", "path", path)
	}
}
