package local

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

// sidecarGit is the slice of gitx the sidecar clone needs. *gitx.Git
// satisfies it; it deliberately has no Commit or Push.
type sidecarGit interface {
	InitBare(ctx context.Context, dir string) error
	FetchShallow(ctx context.Context, repoDir, url, refspec string, token gitx.TokenFunc) error
	RevParse(ctx context.Context, dir, ref string) (string, error)
	ExportTree(ctx context.Context, repoDir, sha, dest string) error
}

// defaultSidecarURL is where a sidecar repo is fetched from.
func defaultSidecarURL(repo string) string { return "https://github.com/" + repo + ".git" }

// sidecarCache is <root>/sidecars/<ctl-owner>/<ctl-repo>/<sc-owner>/<sc-repo>.git:
// one shallow bare object cache per sidecar repo, shared by every job of one
// controlled repo. It holds no remote and no credential.
func (l *Local) sidecarCache(jobRepo, scRepo string) (string, error) {
	base, err := repopath.Join(filepath.Join(l.root, "sidecars"), jobRepo)
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
func (l *Local) lockSidecar(ctx context.Context, dir string) (func(), error) {
	l.mu.Lock()
	if l.sidecarLocks == nil {
		l.sidecarLocks = map[string]chan struct{}{}
	}
	ch := l.sidecarLocks[dir]
	if ch == nil {
		ch = make(chan struct{}, 1)
		l.sidecarLocks[dir] = ch
	}
	l.mu.Unlock()
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
func (l *Local) materialise(ctx context.Context, jobRepo string, s executor.Sidecar, dest string) (sha string, fetched bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, l.sidecarTimeout)
	defer cancel()
	wrap := func(e error) error {
		if errors.Is(e, gitx.ErrRefNotFound) {
			return fmt.Errorf("sidecar %s@%s: %w", s.Repo, s.Ref, e)
		}
		return fmt.Errorf("sidecar %s@%s: %w: %w", s.Repo, s.Ref, executor.ErrClone, e)
	}
	cache, err := l.sidecarCache(jobRepo, s.Repo)
	if err != nil {
		return "", false, fmt.Errorf("sidecar %s@%s: %w", s.Repo, s.Ref, err)
	}
	unlock, err := l.lockSidecar(ctx, cache)
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
		return l.sgit.InitBare(ctx, cache)
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
	url := l.sidecarURL(s.Repo)

	have := false
	if gitref.IsSHA(s.Ref) {
		if got, rerr := l.sgit.RevParse(ctx, cache, s.Ref); rerr == nil && got == s.Ref {
			sha, have = got, true
		}
	}
	if !have {
		fetch := func() error {
			return l.sgit.FetchShallow(ctx, cache, url, "+"+s.Ref+":"+localRef, l.sidecarToken)
		}
		fetched = true
		ferr := fetch()
		if ferr != nil && !errors.Is(ferr, gitx.ErrRefNotFound) && ctx.Err() == nil {
			if rerr := reinit(); rerr != nil {
				return "", fetched, wrap(errors.Join(ferr, rerr))
			}
			ferr = fetch()
		}
		if ferr != nil {
			return "", fetched, wrap(ferr)
		}
		if sha, err = l.sgit.RevParse(ctx, cache, localRef); err != nil {
			return "", fetched, wrap(err)
		}
		if gitref.IsSHA(s.Ref) && sha != s.Ref {
			return "", fetched, wrap(fmt.Errorf("fetched %s, want %s", sha, s.Ref))
		}
	}
	if err := l.sgit.ExportTree(ctx, cache, sha, dest); err != nil {
		return "", fetched, fmt.Errorf("sidecar %s@%s: export: %w", s.Repo, s.Ref, err)
	}
	return sha, fetched, nil
}
