// Package configsrc loads the controller's configuration from the tip of the
// default branch on the remote, not from a working tree (decision #87). Each
// load fetches the branch, resolves it to a SHA and materialises the config
// file and role directories at that SHA into a snapshot directory on disk,
// because roles run from a host path: the config, the role directories and
// the scan worktree all have to describe the same commit.
package configsrc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/config"
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
	Now       func() time.Time
	Log       *slog.Logger
}

// Source loads config from the tip of a branch on the remote. It is safe for
// concurrent use; calls are serialised.
type Source struct {
	o Options

	mu  sync.Mutex
	sha string
	res *config.Resolved
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
		return s.res, nil
	}

	snap := filepath.Join(o.CacheDir, sha)
	fresh := false
	if _, err := os.Stat(snap); errors.Is(err, os.ErrNotExist) {
		if err := s.export(ctx, sha, snap); err != nil {
			return nil, fmt.Errorf("config at %.12s: %w", sha, err)
		}
		fresh = true
	} else if err != nil {
		return nil, fmt.Errorf("config at %.12s: %w", sha, err)
	}
	res, err := config.Load(snap)
	if err != nil {
		// Wrap, so errors.As(err, &config.Errors{}) still works.
		return nil, fmt.Errorf("config at %.12s: %w", sha, err)
	}
	res.SHA = sha
	// The superseded snapshot may still be a running job's role directory:
	// stamp it so retention counts from now, not from when it was exported.
	if s.sha != "" && s.sha != sha {
		now := s.o.Now()
		if err := os.Chtimes(filepath.Join(o.CacheDir, s.sha), now, now); err != nil {
			o.Log.Warn("configsrc.stamp_failed", "sha", s.sha, "error", err)
		}
	}
	s.sha, s.res = sha, res
	if fresh {
		s.prune(sha)
	}
	return res, nil
}

// export writes the source paths at sha into snap through a temporary
// directory, so a crash never leaves a half-written snapshot under its final
// name.
func (s *Source) export(ctx context.Context, sha, snap string) error {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	tmp := snap + ".tmp-" + hex.EncodeToString(b[:])
	if err := s.o.Git.ExportPaths(ctx, s.o.RepoDir, sha, tmp, config.SourcePaths()...); err != nil {
		return err
	}
	if err := os.Rename(tmp, snap); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("publish snapshot: %w", err)
	}
	return nil
}

// prune removes the snapshots other than current that were last current (or
// exported) more than Keep ago, judged by directory modification time.
func (s *Source) prune(current string) {
	ents, err := os.ReadDir(s.o.CacheDir)
	if err != nil {
		s.o.Log.Warn("configsrc.prune_failed", "error", err)
		return
	}
	cutoff := s.o.Now().Add(-s.o.Keep)
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
