package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/gitref"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/repopath"
)

// sidecarDirName is the sidecar tree inside the job home.
const sidecarDirName = "sidecar"

const defaultSidecarTimeout = 2 * time.Minute

// SidecarGit is the slice of gitx the sidecar clone needs. *gitx.Git
// satisfies it; it deliberately has no Commit or Push.
type SidecarGit interface {
	InitBare(ctx context.Context, dir string) error
	FetchShallow(ctx context.Context, repoDir, url, refspec string, token gitx.TokenFunc) error
	RevParse(ctx context.Context, dir, ref string) (string, error)
	ExportTree(ctx context.Context, repoDir, sha, dest string) error
}

// defaultSidecarURL is where a sidecar repo is fetched from.
func defaultSidecarURL(repo string) string { return "https://github.com/" + repo + ".git" }

// SidecarCache is <root>/sidecars/<ctl-owner>/<ctl-repo>/<sc-owner>/<sc-repo>.git:
// one shallow bare object cache per sidecar repo, shared by every job of one
// controlled repo. It holds no remote and no credential.
func SidecarCache(root, jobRepo, scRepo string) (string, error) {
	base, err := repopath.Join(filepath.Join(root, "sidecars"), jobRepo)
	if err != nil {
		return "", err
	}
	dir, err := repopath.Join(base, scRepo)
	if err != nil {
		return "", err
	}
	return dir + ".git", nil
}

// lockSidecar serialises git operations on one cache dir inside this
// process. One serve process owns each controlled repo (the leader lock), so
// no other process touches the cache. A waiter honours ctx.
func (w *Workspace) lockSidecar(ctx context.Context, dir string) (func(), error) {
	w.mu.Lock()
	ch := w.sidecarLocks[dir]
	if ch == nil {
		ch = make(chan struct{}, 1)
		w.sidecarLocks[dir] = ch
	}
	w.mu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// materialise exports s at its resolved commit into dest and returns the SHA
// and whether the network was used. It is serialised per cache dir.
//
// A 40-hex ref already in the cache needs no network. Any other ref is
// fetched (depth 1) on every call. A fetch failure other than a missing ref
// rebuilds the cache once and retries, which clears a lock left behind by a
// crash. Errors: a missing ref is gitx.ErrRefNotFound (permanent); init,
// fetch and rev-parse failures wrap executor.ErrClone (transient); an export
// failure (symlink, submodule, size) wraps neither.
func (w *Workspace) materialise(ctx context.Context, jobRepo string, s executor.Sidecar, dest string) (sha string, fetched bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, w.sidecarTimeout)
	defer cancel()
	wrap := func(e error) error {
		if permanentFetch(e) {
			return fmt.Errorf("sidecar %q (%s@%s): %w", s.Name, s.Repo, s.Ref, e)
		}
		return fmt.Errorf("sidecar %q (%s@%s): %w: %w", s.Name, s.Repo, s.Ref, executor.ErrClone, e)
	}
	cache, err := SidecarCache(w.root, jobRepo, s.Repo)
	if err != nil {
		return "", false, fmt.Errorf("sidecar %q (%s@%s): %w", s.Name, s.Repo, s.Ref, err)
	}
	unlock, err := w.lockSidecar(ctx, cache)
	if err != nil {
		return "", false, wrap(err)
	}
	defer unlock()

	reinit := func() error {
		if err := os.RemoveAll(cache); err != nil {
			return err
		}
		if err := os.MkdirAll(cache, 0o700); err != nil {
			return err
		}
		return w.sgit.InitBare(ctx, cache)
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", false, wrap(err)
	}
	if _, serr := os.Stat(filepath.Join(cache, "HEAD")); serr != nil { // first use, or a crash mid-init
		if err := reinit(); err != nil {
			return "", false, wrap(err)
		}
	}

	// No user text becomes a ref path: the local ref is keyed by a hash.
	key := s.Ref
	if !gitref.IsSHA(s.Ref) {
		sum := sha256.Sum256([]byte(s.Ref))
		key = hex.EncodeToString(sum[:])[:16]
	}
	localRef := "refs/ard/sidecar/" + key
	url := w.sidecarURL(s.Repo)

	have := false
	if gitref.IsSHA(s.Ref) {
		if got, rerr := w.sgit.RevParse(ctx, cache, s.Ref); rerr == nil && got == s.Ref {
			sha, have = got, true
		}
	}
	if !have {
		fetch := func() error {
			if gitref.IsSHA(s.Ref) {
				return FetchPinned(ctx, w.sgit, cache, url, s.Ref, w.sidecarToken)
			}
			return w.sgit.FetchShallow(ctx, cache, url, "+"+s.Ref+":"+localRef, w.sidecarToken)
		}
		fetched = true
		ferr := fetch()
		if ferr != nil && !permanentFetch(ferr) && ctx.Err() == nil {
			if rerr := reinit(); rerr != nil {
				return "", fetched, wrap(errors.Join(ferr, rerr))
			}
			ferr = fetch()
		}
		if ferr != nil {
			return "", fetched, wrap(ferr)
		}
		if gitref.IsSHA(s.Ref) {
			sha = s.Ref // FetchPinned verified it
		} else if sha, err = w.sgit.RevParse(ctx, cache, localRef); err != nil {
			return "", fetched, wrap(err)
		}
	}
	if err := w.sgit.ExportTree(ctx, cache, sha, dest); err != nil {
		return "", fetched, fmt.Errorf("sidecar %q (%s@%s): export: %w", s.Name, s.Repo, s.Ref, err)
	}
	return sha, fetched, nil
}

// permanentFetch reports a fetch failure a retry cannot fix: a missing ref
// or a repository that is missing or unreadable.
func permanentFetch(err error) bool {
	return errors.Is(err, gitx.ErrRefNotFound) || errors.Is(err, gitx.ErrRepoAccess)
}

// FetchPinned fetches the commit sha (depth 1) from url into the bare repo
// gitDir, creating it first when it has no HEAD, and checks that the object
// is the one asked for. A SHA the remote lacks gives gitx.ErrRefNotFound.
func FetchPinned(ctx context.Context, g SidecarGit, gitDir, url, sha string, tok gitx.TokenFunc) error {
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(gitDir, "HEAD")); err != nil {
		if err := g.InitBare(ctx, gitDir); err != nil {
			return err
		}
	}
	if err := g.FetchShallow(ctx, gitDir, url, "+"+sha+":refs/ard/pin", tok); err != nil {
		return err
	}
	got, err := g.RevParse(ctx, gitDir, sha)
	if err != nil {
		return err
	}
	if got != sha {
		return fmt.Errorf("fetched %s, want %s", got, sha)
	}
	return nil
}
