// Package configsrc loads the controller's configuration from the tip of the
// default branch on the remote, not from a working tree (decision #87). Each
// load fetches the branch, resolves it to a SHA and materialises the config
// file and role directories at that SHA into a snapshot directory on disk,
// because roles run from a host path: the config, the role directories and
// the scan worktree all have to describe the same commit.
package configsrc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/gitref"
	"github.com/albertcbrown/ard/internal/gitx"
)

// DefaultKeep is how long a snapshot of a superseded SHA is kept. It is far
// beyond any job timeout, so a running host-mode job keeps its role directory.
const DefaultKeep = 24 * time.Hour

// Git is the slice of gitx the source needs; *gitx.Git satisfies it.
type Git interface {
	FetchRef(ctx context.Context, repoDir, url, refspec string, token gitx.TokenFunc) error
	RevParse(ctx context.Context, dir, ref string) (string, error)
	ExportPaths(ctx context.Context, repoDir, sha, dest string, paths ...string) error
	InitBare(ctx context.Context, dir string) error
	FetchShallow(ctx context.Context, repoDir, url, refspec string, token gitx.TokenFunc) error
}

// Options configure a Source.
type Options struct {
	Git       Git
	RepoDir   string // absolute; the controller's clone (the object store)
	RemoteURL string
	Token     gitx.TokenFunc // nil in tests
	Branch    string         // default "main"
	CacheDir  string         // absolute; snapshots go in CacheDir/<sha>
	Keep      time.Duration  // snapshot retention; default DefaultKeep
	// SharedDir is the bare cache for the roles_repo fetch; absolute, outside
	// CacheDir. Required only when the config sets roles_repo.
	SharedDir string
	// SharedURL maps "owner/name" to a clone URL; default GitHub over https.
	SharedURL func(repo string) string
	Now       func() time.Time
	Log       *slog.Logger
}

// Source loads config from the tip of a branch on the remote. It is safe for
// concurrent use; calls are serialised.
type Source struct {
	o Options

	mu   sync.Mutex
	sha  string
	snap string // directory name of the current snapshot under CacheDir
	res  *config.Resolved
}

// New validates o, creates the cache directory and removes the partial
// snapshots a crashed run left behind.
func New(o Options) (*Source, error) {
	switch {
	case o.Git == nil:
		return nil, errors.New("configsrc: nil Git")
	case !filepath.IsAbs(o.RepoDir):
		return nil, fmt.Errorf("configsrc: RepoDir %q is not absolute", o.RepoDir)
	case !filepath.IsAbs(o.CacheDir):
		return nil, fmt.Errorf("configsrc: CacheDir %q is not absolute", o.CacheDir)
	case o.RemoteURL == "":
		return nil, errors.New("configsrc: empty RemoteURL")
	}
	if o.Branch == "" {
		o.Branch = "main"
	}
	if o.Keep <= 0 {
		o.Keep = DefaultKeep
	}
	if o.SharedURL == nil {
		o.SharedURL = func(repo string) string { return "https://github.com/" + repo + ".git" }
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if err := os.MkdirAll(o.CacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("configsrc: %w", err)
	}
	ents, err := os.ReadDir(o.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("configsrc: %w", err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp-") {
			_ = os.RemoveAll(filepath.Join(o.CacheDir, e.Name()))
		}
	}
	return &Source{o: o}, nil
}

// Load fetches the branch into refs/ard/config/<branch> in RepoDir, resolves
// it to a SHA and returns the config at that SHA, with Resolved.Repo set to
// the snapshot directory CacheDir/<sha> and Resolved.SHA to the SHA. An
// unchanged SHA returns the cached *Resolved without exporting again. Any
// error (fetch, resolve, export, validation) returns (nil, err); callers
// treat it as paused. The fetch is a read, so it runs under --dry-run too.
func (s *Source) Load(ctx context.Context) (*config.Resolved, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.o

	ref := "refs/ard/config/" + o.Branch
	if err := o.Git.FetchRef(ctx, o.RepoDir, o.RemoteURL, "+refs/heads/"+o.Branch+":"+ref, o.Token); err != nil {
		return nil, fmt.Errorf("config source: fetch %s: %w", o.Branch, err)
	}
	sha, err := o.Git.RevParse(ctx, o.RepoDir, ref)
	if err != nil {
		return nil, fmt.Errorf("config source: resolve %s: %w", o.Branch, err)
	}
	if s.res != nil && s.sha == sha {
		if s.res.RolesRepo == nil {
			return s.res, nil
		}
		// The local commit is unchanged, but a branch or tag of the shared
		// repo may have moved.
		rsha, err := s.fetchShared(ctx, *s.res.RolesRepo)
		if err != nil {
			return nil, err
		}
		if rsha == s.res.RolesSHA {
			return s.res, nil
		}
	}

	name, fresh, rsha, err := s.publish(ctx, sha)
	if err != nil {
		return nil, fmt.Errorf("config at %.12s: %w", sha, err)
	}
	snap := filepath.Join(o.CacheDir, name)
	res, err := config.Load(snap)
	if err != nil {
		// Wrap, so errors.As(err, &config.Errors{}) still works.
		return nil, fmt.Errorf("config at %.12s: %w", sha, err)
	}
	res.SHA = sha
	res.RolesSHA = rsha
	// The superseded snapshot may still be a running job's role directory:
	// stamp it so retention counts from now, not from when it was exported.
	prev := s.res
	if s.snap != "" && s.snap != name {
		now := s.o.Now()
		if err := os.Chtimes(filepath.Join(o.CacheDir, s.snap), now, now); err != nil {
			o.Log.Warn("configsrc.stamp_failed", "sha", s.snap, "error", err)
		}
	}
	s.snap = name
	s.sha, s.res = sha, res
	if fresh {
		// A job can run as long as its role's timeout, which may exceed Keep.
		keep := max(o.Keep, longestTimeout(prev, res)+time.Hour)
		s.prune(name, keep)
	}
	return res, nil
}

// publish makes the snapshot for the local commit sha and returns its
// directory name, whether it was newly written and the shared roles commit
// ("" when the config has no roles_repo). The name is sha, or sha+<12 hex of
// the shared commit>. The snapshot is built in a temporary directory,
// validated and renamed into place, so a crash or an invalid shared role
// never leaves a snapshot under its final name.
func (s *Source) publish(ctx context.Context, sha string) (name string, fresh bool, rsha string, err error) {
	plain := filepath.Join(s.o.CacheDir, sha)
	if _, err := os.Stat(plain); err == nil {
		return sha, false, "", nil // only a config without roles_repo is published under the bare sha
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, "", err
	}
	// A fixed name keeps error text stable across ticks (the status issue gets
	// one comment per distinct error). s.mu and the leader lock serialise use.
	tmp := plain + ".tmp-export"
	_ = os.RemoveAll(tmp)
	if err := s.o.Git.ExportPaths(ctx, s.o.RepoDir, sha, tmp, config.SourcePaths()...); err != nil {
		return "", false, "", err
	}
	discard := func() { _ = os.RemoveAll(tmp) }
	rr, err := config.SharedRoles(os.DirFS(tmp))
	if err != nil || rr == nil {
		if err != nil {
			discard()
			return "", false, "", err
		}
		if err := os.Rename(tmp, plain); err != nil {
			discard()
			return "", false, "", fmt.Errorf("publish snapshot: %w", err)
		}
		return sha, true, "", nil
	}
	if rsha, err = s.fetchShared(ctx, *rr); err != nil {
		discard()
		return "", false, "", err
	}
	name = sha + "+" + rsha[:12]
	final := filepath.Join(s.o.CacheDir, name)
	if _, err := os.Stat(final); err == nil {
		discard()
		return name, false, rsha, nil
	}
	if err := s.mergeShared(ctx, rsha, tmp); err != nil {
		discard()
		return "", false, "", err
	}
	if _, err := config.Load(tmp); err != nil {
		discard()
		return "", false, "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		discard()
		return "", false, "", fmt.Errorf("publish snapshot: %w", err)
	}
	return name, true, rsha, nil
}

// fetchShared fetches the pinned ref of the shared roles repo into SharedDir
// and returns its commit SHA.
func (s *Source) fetchShared(ctx context.Context, rr config.Sidecar) (string, error) {
	o := s.o
	wrap := func(err error) error {
		return fmt.Errorf("roles_repo %s@%s: %w", rr.Repo, rr.Ref, err)
	}
	if !filepath.IsAbs(o.SharedDir) {
		return "", wrap(errors.New("no absolute SharedDir configured"))
	}
	if _, err := os.Stat(filepath.Join(o.SharedDir, "HEAD")); err != nil {
		if err := o.Git.InitBare(ctx, o.SharedDir); err != nil {
			return "", wrap(err)
		}
	}
	const local = "refs/ard/roles"
	if err := o.Git.FetchShallow(ctx, o.SharedDir, o.SharedURL(rr.Repo), "+"+rr.Ref+":"+local, o.Token); err != nil {
		return "", wrap(err)
	}
	rsha, err := o.Git.RevParse(ctx, o.SharedDir, local)
	if err != nil {
		return "", wrap(err)
	}
	if gitref.IsSHA(rr.Ref) && rsha != rr.Ref {
		return "", wrap(fmt.Errorf("fetched %s, want %s", rsha, rr.Ref))
	}
	return rsha, nil
}

// mergeShared copies each role directory of the shared commit rsha into dir
// unless dir already has a directory of that kind and name: the local
// directory wins whole, never file by file.
func (s *Source) mergeShared(ctx context.Context, rsha, dir string) error {
	ex := filepath.Join(s.o.CacheDir, filepath.Base(dir)+"-shared")
	_ = os.RemoveAll(ex)
	defer os.RemoveAll(ex)
	if err := s.o.Git.ExportPaths(ctx, s.o.SharedDir, rsha, ex, config.SharedSourcePaths()...); err != nil {
		return fmt.Errorf("roles_repo: %w", err)
	}
	kinds, err := os.ReadDir(filepath.Join(ex, ".ard"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	for _, k := range kinds {
		roles, err := os.ReadDir(filepath.Join(ex, ".ard", k.Name()))
		if err != nil {
			continue // a file, not a tree
		}
		for _, r := range roles {
			dst := filepath.Join(dir, ".ard", k.Name(), r.Name())
			if _, err := os.Lstat(dst); err == nil {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.Rename(filepath.Join(ex, ".ard", k.Name(), r.Name()), dst); err != nil {
				return err
			}
		}
	}
	return nil
}

// prune removes the snapshots other than current that were last current (or
// exported) more than Keep ago, judged by directory modification time.
func (s *Source) prune(current string, keep time.Duration) {
	ents, err := os.ReadDir(s.o.CacheDir)
	if err != nil {
		s.o.Log.Warn("configsrc.prune_failed", "error", err)
		return
	}
	cutoff := s.o.Now().Add(-keep)
	for _, e := range ents {
		if !e.IsDir() || e.Name() == current || strings.Contains(e.Name(), ".tmp-") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.o.CacheDir, e.Name())); err != nil {
			s.o.Log.Warn("configsrc.prune_failed", "snapshot", e.Name(), "error", err)
		}
	}
}

// longestTimeout is the largest role timeout across the configs; nil is skipped.
func longestTimeout(rs ...*config.Resolved) time.Duration {
	var d time.Duration
	for _, r := range rs {
		if r == nil {
			continue
		}
		for _, role := range r.Roles {
			d = max(d, role.Timeout)
		}
	}
	return d
}
