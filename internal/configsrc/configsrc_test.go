package configsrc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

// countGit wraps gitx.Git and counts exports.
type countGit struct {
	*gitx.Git
	exports atomic.Int32
}

func (c *countGit) ExportPaths(ctx context.Context, repoDir, sha, dest string, paths ...string) error {
	c.exports.Add(1)
	return c.Git.ExportPaths(ctx, repoDir, sha, dest, paths...)
}

type env struct {
	t      *testing.T
	bare   string
	clone  string // the controller's clone; never pulled
	other  string // a second clone that pushes
	git    *countGit
	cache  string
	source *Source
}

func newEnv(t *testing.T) *env {
	t.Helper()
	bare, clone := gittest.NewRemote(t)
	other := filepath.Join(t.TempDir(), "other")
	gittest.Run(t, filepath.Dir(other), "clone", bare, other)
	e := &env{t: t, bare: bare, clone: clone, other: other, git: &countGit{Git: gitx.New("")}, cache: filepath.Join(t.TempDir(), "cfg")}
	e.source = e.newSource(nil)
	return e
}

func (e *env) newSource(now func() time.Time) *Source {
	s, err := New(Options{
		Git: e.git, RepoDir: e.clone, RemoteURL: e.bare, CacheDir: e.cache,
		Now: now, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

// push commits files (rel path -> content; a leading "*" on the path marks it
// executable) in the other clone and pushes main.
func (e *env) push(files map[string]string) {
	e.t.Helper()
	gittest.Run(e.t, e.other, "pull", "--quiet", "origin", "main")
	for rel, content := range files {
		mode := os.FileMode(0o644)
		if strings.HasPrefix(rel, "*") {
			rel, mode = rel[1:], 0o755
		}
		p := filepath.Join(e.other, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			e.t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			e.t.Fatal(err)
		}
	}
	gittest.Run(e.t, e.other, "add", "-A")
	gittest.Run(e.t, e.other, "commit", "-m", "change")
	gittest.Run(e.t, e.other, "push", "origin", "main")
}

func TestLoadAppliesMergedChangeWithoutPull(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	headBefore := strings.TrimSpace(gittest.Run(t, e.clone, "rev-parse", "HEAD"))

	e.push(map[string]string{".ard/config.yaml": "max_pods: 2\n"})
	res, err := e.source.Load(ctx)
	if err != nil || res.MaxPods != 2 {
		t.Fatalf("first Load = %+v, %v", res, err)
	}
	e.push(map[string]string{".ard/config.yaml": "max_pods: 4\n"})
	res, err = e.source.Load(ctx)
	if err != nil || res.MaxPods != 4 {
		t.Fatalf("second Load = %+v, %v", res, err)
	}
	if res.SHA == "" || res.Repo != filepath.Join(e.cache, res.SHA) {
		t.Errorf("SHA %q, Repo %q", res.SHA, res.Repo)
	}
	if got := strings.TrimSpace(gittest.Run(t, e.clone, "rev-parse", "HEAD")); got != headBefore {
		t.Errorf("clone HEAD moved: %s -> %s", headBefore, got)
	}
	if st := gittest.Run(t, e.clone, "status", "--porcelain"); st != "" {
		t.Errorf("clone working tree changed: %q", st)
	}
	if _, err := os.Stat(filepath.Join(e.clone, ".ard")); !os.IsNotExist(err) {
		t.Errorf("clone gained .ard: %v", err)
	}
}

func TestLoadIgnoresWorkingTree(t *testing.T) {
	e := newEnv(t)
	e.push(map[string]string{".ard/config.yaml": "max_pods: 2\n"})
	if err := os.MkdirAll(filepath.Join(e.clone, ".ard"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.clone, ".ard", "config.yaml"), []byte("executor: bogus\n  : bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.source.Load(context.Background())
	if err != nil || res.MaxPods != 2 {
		t.Fatalf("Load = %+v, %v", res, err)
	}
}

func TestLoadCachesSameSHA(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.push(map[string]string{".ard/config.yaml": "max_pods: 2\n"})
	a, err := e.source.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.source.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("second Load returned a different *Resolved")
	}
	if n := e.git.exports.Load(); n != 1 {
		t.Errorf("exports = %d, want 1", n)
	}
	// A restarted controller reuses the snapshot on disk.
	s2 := e.newSource(nil)
	c, err := s2.Load(ctx)
	if err != nil || c.SHA != a.SHA {
		t.Fatalf("restart Load = %+v, %v", c, err)
	}
	if n := e.git.exports.Load(); n != 1 {
		t.Errorf("exports after restart = %d, want 1", n)
	}
}

func TestLoadInvalidConfigAtTip(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.push(map[string]string{".ard/config.yaml": "max_pods: 2\n"})
	good, err := e.source.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e.push(map[string]string{".ard/config.yaml": "max_pods: 0\n"})
	bad, err := e.source.Load(ctx)
	if bad != nil || err == nil {
		t.Fatalf("Load = %+v, %v; want error", bad, err)
	}
	var cerrs config.Errors
	if !errors.As(err, &cerrs) {
		t.Errorf("error does not wrap config.Errors: %v", err)
	}
	tip := strings.TrimSpace(gittest.Run(t, e.bare, "rev-parse", "main"))
	if !strings.Contains(err.Error(), tip[:12]) {
		t.Errorf("error does not name the short SHA %s: %v", tip[:12], err)
	}
	if _, serr := os.Stat(filepath.Join(good.Repo, ".ard", "config.yaml")); serr != nil {
		t.Errorf("previous snapshot damaged: %v", serr)
	}
}

func TestLoadFetchFailure(t *testing.T) {
	e := newEnv(t)
	s, err := New(Options{Git: e.git, RepoDir: e.clone, RemoteURL: filepath.Join(t.TempDir(), "nope.git"), CacheDir: e.cache})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Load(context.Background())
	if res != nil || err == nil || !strings.Contains(err.Error(), "fetch main") {
		t.Fatalf("Load = %+v, %v", res, err)
	}
}

func TestLoadMaterialisesRoleDirectory(t *testing.T) {
	e := newEnv(t)
	e.push(map[string]string{
		".ard/config.yaml":           "max_pods: 2\n",
		"*.ard/scanners/x/scan":      "#!/bin/sh\n",
		".ard/scanners/x/Dockerfile": "FROM scratch\n",
		".ard/triage/t/prompt.txt":   "route\n",
		".ard/7/scanner.log":         "not exported\n",
	})
	res, err := e.source.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scan := filepath.Join(res.Repo, ".ard", "scanners", "x", "scan")
	info, err := os.Stat(scan)
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("scan: %v, %v", info, err)
	}
	r, ok := res.Role(config.KindScanner, "x")
	if !ok || filepath.Join(res.Repo, r.Dockerfile) == "" {
		t.Fatalf("role x = %+v, %v", r, ok)
	}
	if _, err := os.Stat(filepath.Join(res.Repo, ".ard", "7")); !os.IsNotExist(err) {
		t.Errorf("issue directory was exported: %v", err)
	}
}

func TestPruneAndTmpCleanup(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	now := time.Now()
	clock := &now
	s := e.newSource(func() time.Time { return *clock })

	e.push(map[string]string{".ard/config.yaml": "max_pods: 2\n"})
	first, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e.push(map[string]string{".ard/config.yaml": "max_pods: 3\n"})
	second, err := s.Load(ctx) // stamps first as superseded at *clock
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.Repo); err != nil {
		t.Fatalf("a freshly superseded snapshot was pruned: %v", err)
	}

	later := now.Add(25 * time.Hour)
	clock = &later
	e.push(map[string]string{".ard/config.yaml": "max_pods: 4\n"})
	third, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.Repo); !os.IsNotExist(err) {
		t.Errorf("old snapshot not pruned: %v", err)
	}
	// second was superseded just now (at "later"), so it stays.
	for _, keep := range []string{second.Repo, third.Repo} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("%s pruned: %v", keep, err)
		}
	}

	leftover := filepath.Join(e.cache, "deadbeef.tmp-abc")
	if err := os.MkdirAll(filepath.Join(leftover, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.newSource(nil)
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Errorf("leftover tmp dir survived New: %v", err)
	}
}

func TestConcurrentLoads(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.push(map[string]string{".ard/config.yaml": "max_pods: 2\n", "*.ard/scanners/x/scan": "#!/bin/sh\n"})
	var wg sync.WaitGroup
	var bad atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := e.source.Load(ctx)
			if err != nil || res.MaxPods != 2 {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Errorf("%d concurrent loads failed", bad.Load())
	}
	if n := e.git.exports.Load(); n != 1 {
		t.Errorf("exports = %d, want 1", n)
	}
}
