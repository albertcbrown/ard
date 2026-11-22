//go:build unix

package local

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

// countGit counts FetchShallow calls.
type countGit struct {
	*gitx.Git
	fetches atomic.Int32
}

func (c *countGit) FetchShallow(ctx context.Context, repoDir, url, refspec string, token gitx.TokenFunc) error {
	c.fetches.Add(1)
	return c.Git.FetchShallow(ctx, repoDir, url, refspec, token)
}

type scFixture struct {
	*fixture
	bare, clone string
	cg          *countGit
}

func newSidecarFixture(t *testing.T, mut ...func(*Options)) *scFixture {
	t.Helper()
	f := newFixture(t)
	bare, clone := gittest.NewRemote(t)
	sf := &scFixture{fixture: f, bare: bare, clone: clone, cg: &countGit{Git: gitx.New("")}}
	f.opts.SidecarGit = sf.cg
	f.opts.SidecarURL = func(string) string { return bare }
	for _, m := range mut {
		m(&f.opts)
	}
	l, err := New(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.l = l
	return sf
}

// push commits files to the sidecar remote's main and returns the commit.
func (sf *scFixture) push(t *testing.T, files map[string]string) string {
	t.Helper()
	for rel, c := range files {
		p := filepath.Join(sf.clone, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Run(t, sf.clone, "add", "-A")
	gittest.Run(t, sf.clone, "commit", "-m", "c")
	gittest.Run(t, sf.clone, "push", "origin", "main")
	return strings.TrimSpace(gittest.Run(t, sf.clone, "rev-parse", "HEAD"))
}

func (sf *scFixture) sjob(ref string) executor.Job {
	j := sf.job(time.Minute)
	j.Sidecar = executor.Sidecar{Repo: "acme/specs", Ref: ref}
	return j
}

func (sf *scFixture) cacheDir(t *testing.T) string {
	t.Helper()
	d, err := sf.l.sidecarCache("o/r", "acme/specs")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// assertCleanFailure checks that a failed Prepare left nothing behind.
func (sf *scFixture) assertCleanFailure(t *testing.T, j executor.Job) {
	t.Helper()
	wt, _ := sf.l.WorktreePath(j.Repo, j.ID)
	home, _ := sf.l.homePath(j.Repo, j.ID)
	for _, p := range []string{wt, home} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s remains: %v", p, err)
		}
	}
	if list := sh(t, sf.repo, "git", "worktree", "list"); strings.Count(list, "\n") != 0 {
		t.Errorf("registered worktree left: %s", list)
	}
	logDir, _ := sf.l.logDir(j.Repo, j.Issue)
	if ents, _ := os.ReadDir(logDir); len(ents) != 0 {
		t.Errorf("log files left: %v", ents)
	}
}

func TestSidecarSHAPin(t *testing.T) {
	sf := newSidecarFixture(t)
	a := sf.push(t, map[string]string{"spec.md": "a"})
	sf.push(t, map[string]string{"spec.md": "b"})

	h, st := sf.run(t, sf.sjob(a), agent(t, "env"))
	if st.ExitCode != 0 {
		t.Fatalf("status %+v", st)
	}
	if got := readFile(t, filepath.Join(h.SidecarDir, "spec.md")); got != "a" {
		t.Errorf("spec.md = %q; want the pinned commit", got)
	}
	if h.SidecarSHA != a || h.View.Sidecar != h.SidecarDir || h.SidecarDir != filepath.Join(h.Home, "sidecar") {
		t.Errorf("handle sidecar = %q sha %q view %q", h.SidecarDir, h.SidecarSHA, h.View.Sidecar)
	}
	if h.Sidecar != (executor.Sidecar{Repo: "acme/specs", Ref: a}) {
		t.Errorf("handle Sidecar = %+v", h.Sidecar)
	}
	if out := readFile(t, h.StdoutPath); !strings.Contains(out, "ARD_SIDECAR="+h.SidecarDir+"\n") {
		t.Errorf("ARD_SIDECAR missing from env:\n%s", out)
	}
	if n := sf.cg.fetches.Load(); n != 1 {
		t.Errorf("fetches = %d; want 1", n)
	}

	// The pin is now cached: no network at all, even with the remote gone.
	if err := os.RemoveAll(sf.bare); err != nil {
		t.Fatal(err)
	}
	h2, _ := sf.run(t, sf.sjob(a), agent(t, "ok"))
	if h2.SidecarSHA != a || readFile(t, filepath.Join(h2.SidecarDir, "spec.md")) != "a" {
		t.Errorf("second job: sha %s", h2.SidecarSHA)
	}
	if n := sf.cg.fetches.Load(); n != 1 {
		t.Errorf("fetches after cache hit = %d; want still 1", n)
	}
}

func TestSidecarBranchMoves(t *testing.T) {
	sf := newSidecarFixture(t)
	b := sf.push(t, map[string]string{"spec.md": "b"})
	h1, _ := sf.run(t, sf.sjob("main"), agent(t, "ok"))
	if h1.SidecarSHA != b || readFile(t, filepath.Join(h1.SidecarDir, "spec.md")) != "b" {
		t.Fatalf("job 1 sees sha %s", h1.SidecarSHA)
	}
	c := sf.push(t, map[string]string{"spec.md": "c"})
	h2, _ := sf.run(t, sf.sjob("main"), agent(t, "ok"))
	if h2.SidecarSHA != c || readFile(t, filepath.Join(h2.SidecarDir, "spec.md")) != "c" {
		t.Errorf("job 2 sees sha %s; want %s", h2.SidecarSHA, c)
	}
	if got := readFile(t, filepath.Join(h1.SidecarDir, "spec.md")); got != "b" {
		t.Errorf("job 1 tree changed to %q", got)
	}
	if n := sf.cg.fetches.Load(); n != 2 {
		t.Errorf("fetches = %d; want 1 per job", n)
	}
}

func TestSidecarReadOnlyHost(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores write bits")
	}
	sf := newSidecarFixture(t)
	a := sf.push(t, map[string]string{"spec.md": "a", "dir/deep/x.md": "x"})
	h, st := sf.run(t, sf.sjob(a), agent(t, "sidecar-write"))
	if st.ExitCode == 0 {
		t.Error("write into the sidecar succeeded")
	}
	if _, err := os.Stat(filepath.Join(h.SidecarDir, "probe")); err == nil {
		t.Error("probe exists")
	}
	filepath.WalkDir(h.SidecarDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		if info, _ := d.Info(); info.Mode().Perm()&0o222 != 0 {
			t.Errorf("%s is writable: %v", p, info.Mode())
		}
		return nil
	})
	if _, err := os.Lstat(filepath.Join(h.SidecarDir, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git in the sidecar tree: %v", err)
	}
	// Cleanup removes the read-only tree; KeepWorktree keeps it.
	ctx := context.Background()
	if err := sf.l.Cleanup(ctx, h, executor.CleanupOptions{KeepWorktree: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.SidecarDir); err != nil {
		t.Errorf("KeepWorktree removed the sidecar: %v", err)
	}
	if err := sf.l.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.Home); !os.IsNotExist(err) {
		t.Errorf("home remains: %v", err)
	}
}

func TestSidecarFailures(t *testing.T) {
	sf := newSidecarFixture(t)
	good := sf.push(t, map[string]string{"spec.md": "a"})
	// A commit with a symlink, on its own branch.
	gittest.Run(t, sf.clone, "checkout", "-q", "-b", "linky")
	if err := os.Symlink("spec.md", filepath.Join(sf.clone, "link.md")); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, sf.clone, "add", "-A")
	gittest.Run(t, sf.clone, "commit", "-m", "link")
	gittest.Run(t, sf.clone, "push", "origin", "linky")
	_ = good

	tests := []struct {
		name      string
		ref       string
		wantIs    error
		wantNotIs error
		wantText  string
	}{
		{"unknown branch", "nope", gitx.ErrRefNotFound, executor.ErrClone, ""},
		{"unknown sha", strings.Repeat("1", 40), gitx.ErrRefNotFound, executor.ErrClone, ""},
		{"symlink", "linky", nil, executor.ErrClone, "link.md"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			j := sf.sjob(tc.ref)
			_, err := sf.l.Prepare(context.Background(), j)
			if err == nil {
				t.Fatal("Prepare succeeded")
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("err = %v; want Is %v", err, tc.wantIs)
			}
			if errors.Is(err, tc.wantNotIs) {
				t.Errorf("err = %v; must not be %v", err, tc.wantNotIs)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("err = %v; want text %q", err, tc.wantText)
			}
			sf.assertCleanFailure(t, j)
		})
	}
}

func TestSidecarUnreachableRemote(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.git")
	sf := newSidecarFixture(t, func(o *Options) {
		o.SidecarURL = func(string) string { return missing }
		o.SidecarToken = func(context.Context) (string, error) { return "s3cr3t-tok", nil }
	})
	j := sf.sjob("main")
	_, err := sf.l.Prepare(context.Background(), j)
	if !errors.Is(err, executor.ErrClone) || errors.Is(err, gitx.ErrRefNotFound) {
		t.Fatalf("err = %v; want ErrClone only", err)
	}
	if strings.Contains(err.Error(), "s3cr3t-tok") {
		t.Errorf("token in error: %v", err)
	}
	// One attempt plus the rebuild-and-retry.
	if n := sf.cg.fetches.Load(); n != 2 {
		t.Errorf("fetches = %d; want 2", n)
	}
	sf.assertCleanFailure(t, j)
}

func TestSidecarTokenNeverLeaks(t *testing.T) {
	sf := newSidecarFixture(t, func(o *Options) {
		o.SidecarToken = func(context.Context) (string, error) { return "s3cr3t-tok", nil }
	})
	sf.push(t, map[string]string{"spec.md": "a"})
	h, _ := sf.run(t, sf.sjob("main"), agent(t, "env"))
	const secret = "s3cr3t-tok"
	b, _ := json.Marshal(h)
	if strings.Contains(string(b), secret) {
		t.Error("token in the Handle")
	}
	for _, kv := range sf.l.hostEnv(h) {
		if strings.Contains(kv, secret) {
			t.Errorf("token in job env: %s", kv)
		}
	}
	if strings.Contains(readFile(t, h.StdoutPath), secret) {
		t.Error("token in the job's environment output")
	}
	filepath.WalkDir(sf.cacheDir(t), func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && (d.Name() == "config" || d.Name() == "shallow") {
			if strings.Contains(readFile(t, p), secret) {
				t.Errorf("token in %s", p)
			}
		}
		return nil
	})
}

func TestSidecarConcurrentPrepare(t *testing.T) {
	sf := newSidecarFixture(t)
	sf.push(t, map[string]string{"spec.md": "a"})
	const n = 8
	var wg sync.WaitGroup
	handles := make([]executor.Handle, n)
	errs := make([]error, n)
	jobs := make([]executor.Job, n)
	for i := range jobs {
		jobs[i] = sf.sjob("main")
		jobs[i].Issue = 100 + i // distinct log names
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handles[i], errs[i] = sf.l.Prepare(context.Background(), jobs[i])
		}()
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Errorf("job %d: %v", i, errs[i])
			continue
		}
		t.Cleanup(func() { sf.l.Cleanup(context.Background(), handles[i], executor.CleanupOptions{}) })
	}
	ents, err := os.ReadDir(filepath.Dir(sf.cacheDir(t)))
	if err != nil || len(ents) != 1 {
		t.Errorf("cache dirs = %v, %v; want exactly one", ents, err)
	}
}

func TestSidecarCrashMidInit(t *testing.T) {
	sf := newSidecarFixture(t)
	a := sf.push(t, map[string]string{"spec.md": "a"})
	cache := sf.cacheDir(t)
	if err := os.MkdirAll(filepath.Join(cache, "objects"), 0o700); err != nil { // no HEAD
		t.Fatal(err)
	}
	h, _ := sf.run(t, sf.sjob("main"), agent(t, "ok"))
	if h.SidecarSHA != a {
		t.Errorf("sha = %s; want %s", h.SidecarSHA, a)
	}
}

func TestSidecarStaleLockRecovery(t *testing.T) {
	sf := newSidecarFixture(t)
	sf.push(t, map[string]string{"spec.md": "a"})
	sf.run(t, sf.sjob("main"), agent(t, "ok"))
	c := sf.push(t, map[string]string{"spec.md": "c"})
	if err := os.WriteFile(filepath.Join(sf.cacheDir(t), "shallow.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	before := sf.cg.fetches.Load()
	h, _ := sf.run(t, sf.sjob("main"), agent(t, "ok"))
	if h.SidecarSHA != c {
		t.Errorf("sha = %s; want %s", h.SidecarSHA, c)
	}
	if n := sf.cg.fetches.Load() - before; n != 2 {
		t.Errorf("fetch calls = %d; want 2 (failed, then rebuilt)", n)
	}
}

func TestSidecarFailureAfterExport(t *testing.T) {
	sf := newSidecarFixture(t, func(o *Options) { o.DockerBin = "/bin/false" })
	sf.push(t, map[string]string{"spec.md": "a"})
	if err := os.WriteFile(filepath.Join(sf.roleDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	j := sf.sjob("main")
	j.Dockerfile = filepath.Join(sf.roleDir, "Dockerfile")
	if _, err := sf.l.Prepare(context.Background(), j); err == nil {
		t.Fatal("Prepare succeeded with a failing docker")
	}
	sf.assertCleanFailure(t, j)
}

func TestSidecarOldHandle(t *testing.T) {
	sf := newSidecarFixture(t)
	h, _ := sf.run(t, sf.job(time.Minute), agent(t, "ok"))
	var m map[string]any
	b, _ := json.Marshal(h)
	json.Unmarshal(b, &m)
	delete(m, "Sidecar")
	delete(m, "SidecarSHA")
	b, _ = json.Marshal(m)
	var old executor.Handle
	if err := json.Unmarshal(b, &old); err != nil {
		t.Fatal(err)
	}
	if old.SidecarSHA != "" || old.Sidecar != (executor.Sidecar{}) {
		t.Errorf("old handle = %+v", old)
	}
	if err := sf.l.Cleanup(context.Background(), old, executor.CleanupOptions{}); err != nil {
		t.Fatal(err)
	}
}

// accessGit fails every fetch with gitx.ErrRepoAccess.
type accessGit struct {
	*countGit
}

func (a accessGit) FetchShallow(ctx context.Context, repoDir, url, refspec string, token gitx.TokenFunc) error {
	a.fetches.Add(1)
	return gitx.ErrRepoAccess
}

func TestSidecarRepoAccessIsPermanent(t *testing.T) {
	sf := newSidecarFixture(t)
	ag := accessGit{sf.cg}
	sf.opts.SidecarGit = ag
	l, err := New(sf.opts)
	if err != nil {
		t.Fatal(err)
	}
	sf.l = l
	j := sf.sjob("main")
	j.Sidecar.Name = "vpe-specs"
	_, err = sf.l.Prepare(context.Background(), j)
	if !errors.Is(err, gitx.ErrRepoAccess) || errors.Is(err, executor.ErrClone) {
		t.Fatalf("err = %v; want ErrRepoAccess and not ErrClone", err)
	}
	if !strings.Contains(err.Error(), `"vpe-specs"`) {
		t.Errorf("error does not name the sidecar key: %v", err)
	}
	if n := sf.cg.fetches.Load(); n != 1 {
		t.Errorf("fetches = %d; want 1", n)
	}
	sf.assertCleanFailure(t, j)
}
