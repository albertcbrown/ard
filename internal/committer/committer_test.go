package committer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/local"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

var bot = github.Identity{Login: "ard[bot]", ID: 4242, Kind: github.AuthApp}

type env struct {
	bare, clone, work string
	cfg               Config
	g                 *gitx.Git
}

func newEnv(t *testing.T) *env {
	t.Helper()
	bare, clone := gittest.NewRemote(t)
	// A dedicated clone supplies the object store, as the executor's does.
	e := &env{bare: bare, clone: clone, work: t.TempDir(), g: gitx.New("")}
	e.cfg = Config{
		RepoDir: clone, WorkDir: e.work, RemoteURL: "file://" + bare, Bot: bot,
		Backoff: func(int) time.Duration { return 0 },
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return e
}

func (e *env) live() *effects.Live {
	return effects.NewLive(fake.New(), e.g, e.cfg.Log)
}

func (e *env) main(t *testing.T, fx Effects) *Committer {
	t.Helper()
	c, err := NewMain(context.Background(), e.cfg, fx, e.g)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func (e *env) head(t *testing.T, ref string) string {
	return strings.TrimSpace(gittest.Run(t, e.bare, "rev-parse", ref))
}

func req(issue int, name, body string) Request {
	return Request{Issue: issue, Stage: "triage", Role: "lead", Summary: "update " + name,
		Files: []File{{Path: fmt.Sprintf(".ard/%d/%s", issue, name), Data: []byte(body)}}}
}

func TestCommitHappyNoOpDelete(t *testing.T) {
	e := newEnv(t)
	// .gitignore ignores logs; the committer still commits them.
	gittest.Run(t, e.clone, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(e.clone, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, e.clone, "add", ".gitignore")
	gittest.Run(t, e.clone, "commit", "-q", "-m", "ignore")
	gittest.Run(t, e.clone, "push", "-q", "origin", "main")
	// Hostile host config and hooks must not matter.
	hooks := filepath.Join(e.bare, "hooks")
	_ = os.MkdirAll(hooks, 0o755)
	t.Setenv("HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".gitconfig"), []byte("[commit]\n\tgpgsign = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"pre-commit", "pre-push"} {
		if err := os.WriteFile(filepath.Join(e.clone, ".git", "hooks", h), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	c := e.main(t, e.live())
	ctx := context.Background()
	r := req(12, "issue.md", "hello")
	r.JobID = "JOB1"
	r.Files = append(r.Files, File{Path: ".ard/12/logs/w.log", Data: []byte("log")})
	res, err := c.Commit(ctx, r)
	if err != nil || res.NoOp || res.Attempts != 1 {
		t.Fatalf("commit: %+v, %v", res, err)
	}
	if got := e.head(t, "main"); got != res.SHA {
		t.Fatalf("remote main %s, result %s", got, res.SHA)
	}
	log := gittest.Run(t, e.bare, "log", "-1", "--format=%an <%ae>|%cn <%ce>|%B", "main")
	if !strings.HasPrefix(log, "ard[bot] <4242+ard[bot]@users.noreply.github.com>|ard[bot] <4242+ard[bot]@users.noreply.github.com>|ard(triage:lead): #12 update issue.md [skip ci]") ||
		!strings.Contains(log, "Ard-Job: JOB1") {
		t.Fatalf("log = %q", log)
	}
	files := strings.Fields(gittest.Run(t, e.bare, "show", "--name-only", "--format=", "main"))
	if fmt.Sprint(files) != "[.ard/12/issue.md .ard/12/logs/w.log]" {
		t.Fatalf("files = %v", files)
	}

	// Replay: unchanged tree is a no-op that returns HEAD.
	res2, err := c.Commit(ctx, r)
	if err != nil || !res2.NoOp || res2.SHA != res.SHA {
		t.Fatalf("replay: %+v, %v", res2, err)
	}
	if n := strings.TrimSpace(gittest.Run(t, e.bare, "rev-list", "--count", "main")); n != "3" {
		t.Fatalf("commits = %s", n)
	}

	// Delete a tracked file; deleting an absent file is a no-op.
	del := Request{Issue: 12, Stage: "triage", Role: "lead", Summary: "drop", Files: []File{{Path: ".ard/12/issue.md", Delete: true}}}
	if res, err := c.Commit(ctx, del); err != nil || res.NoOp {
		t.Fatalf("delete: %+v, %v", res, err)
	}
	if res, err := c.Commit(ctx, del); err != nil || !res.NoOp {
		t.Fatalf("delete again: %+v, %v", res, err)
	}
}

func TestCommitRejections(t *testing.T) {
	e := newEnv(t)
	c := e.main(t, e.live())
	before := e.head(t, "main")
	bad := req(12, "x", "x")
	bad.Files = append(bad.Files, File{Path: ".ard/config.yaml", Data: []byte("x")})
	if _, err := c.Commit(context.Background(), bad); !errors.Is(err, ErrGuard) {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.Commit(context.Background(), req(-1, "x", "x")); !errors.Is(err, ErrGuard) {
		t.Fatalf("negative issue live: %v", err)
	}
	if e.head(t, "main") != before {
		t.Fatal("remote moved")
	}
}

// racer wraps Effects; its Push first lands competing commits from a second
// clone, k times, then delegates.
type racer struct {
	Effects
	e      *env
	t      *testing.T
	k      int
	path   string
	pushes atomic.Int32
}

func (r *racer) Push(ctx context.Context, op effects.PushOp) error {
	r.pushes.Add(1)
	if r.k > 0 {
		r.k--
		rival := filepath.Join(r.e.work, fmt.Sprintf("rival%d", r.k))
		gittest.Run(r.t, r.e.work, "clone", "-q", "file://"+r.e.bare, rival)
		p := filepath.Join(rival, filepath.FromSlash(r.path))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(fmt.Sprint("rival", r.k)), 0o644)
		gittest.Run(r.t, rival, "add", "-A")
		gittest.Run(r.t, rival, "commit", "-q", "-m", "rival")
		gittest.Run(r.t, rival, "push", "-q", "origin", "HEAD:main")
	}
	return r.Effects.Push(ctx, op)
}

func TestRetryAndConflict(t *testing.T) {
	tests := []struct {
		name    string
		k       int
		path    string
		wantErr error
		pushes  int32
	}{
		{"one race", 1, "other/file", nil, 2},
		{"exhausted", 4, "other/file", effects.ErrStaleLease, 4},
		{"conflict", 1, ".ard/12/issue.md", ErrConflict, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			r := &racer{Effects: e.live(), e: e, t: t, k: tc.k, path: tc.path}
			c := e.main(t, r)
			res, err := c.Commit(context.Background(), req(12, "issue.md", "mine"))
			if !errors.Is(err, tc.wantErr) && !(tc.wantErr == effects.ErrStaleLease && errors.Is(err, effects.ErrNonFastForward)) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got := r.pushes.Load(); got != tc.pushes {
				t.Fatalf("pushes = %d, want %d", got, tc.pushes)
			}
			if st := gittest.Run(t, c.l.wt, "status", "--porcelain"); st != "" {
				t.Fatalf("worktree dirty: %q", st)
			}
			if tc.wantErr == nil {
				if res.Attempts != 2 {
					t.Fatalf("attempts = %d", res.Attempts)
				}
				gittest.Run(t, e.bare, "merge-base", "--is-ancestor", "main~1", "main")
				if out := gittest.Run(t, e.bare, "show", "main:.ard/12/issue.md"); out != "mine" {
					t.Fatalf("content %q", out)
				}
				if n := strings.TrimSpace(gittest.Run(t, e.bare, "rev-list", "--count", "--merges", "main")); n != "0" {
					t.Fatal("merge commit")
				}
			}
		})
	}
}

func TestRemoteRejectedNotRetried(t *testing.T) {
	e := newEnv(t)
	hook := filepath.Join(e.bare, "hooks", "pre-receive")
	_ = os.MkdirAll(filepath.Dir(hook), 0o755)
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &racer{Effects: e.live(), e: e, t: t}
	c := e.main(t, r)
	_, err := c.Commit(context.Background(), req(12, "issue.md", "x"))
	if err == nil || errors.Is(err, effects.ErrNonFastForward) || r.pushes.Load() != 1 {
		t.Fatalf("err = %v, pushes = %d", err, r.pushes.Load())
	}
}

func TestTokenScrubbed(t *testing.T) {
	e := newEnv(t)
	e.cfg.RemoteURL = "https://127.0.0.1:1/x.git"
	e.cfg.Token = func(context.Context) (string, error) { return "s3cr3t", nil }
	_, err := NewMain(context.Background(), e.cfg, e.live(), e.g)
	if err == nil || strings.Contains(err.Error(), "s3cr3t") || strings.Contains(err.Error(), "czNjcjN0") {
		t.Fatalf("err = %v", err)
	}
}

func TestConcurrent(t *testing.T) {
	e := newEnv(t)
	c := e.main(t, e.live())
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := 1; i <= 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Commit(context.Background(), req(i, "issue.md", fmt.Sprint(i)))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.TrimSpace(gittest.Run(t, e.bare, "rev-list", "--count", "--no-merges", "main")); n != "51" {
		t.Fatalf("commits = %s", n)
	}
	if n := strings.TrimSpace(gittest.Run(t, e.bare, "rev-list", "--count", "--merges", "main")); n != "0" {
		t.Fatal("merges")
	}
	if n := len(strings.Fields(gittest.Run(t, e.bare, "ls-tree", "-r", "--name-only", "main"))); n != 50 {
		t.Fatalf("files = %d", n)
	}
}

func TestDryRun(t *testing.T) {
	e := newEnv(t)
	d, err := effects.NewDryRun(effects.DryRunOptions{Log: e.cfg.Log, Staged: e.g.StagedPaths})
	if err != nil {
		t.Fatal(err)
	}
	e.cfg.Bot = github.Identity{}
	c := e.main(t, d)
	before := e.head(t, "main")
	res, err := c.Commit(context.Background(), req(12, "issue.md", "x"))
	if err != nil || !res.Synthetic || res.SHA == "" {
		t.Fatalf("%+v, %v", res, err)
	}
	if e.head(t, "main") != before {
		t.Fatal("remote moved")
	}
	if st := gittest.Run(t, c.l.wt, "status", "--porcelain"); st != "" {
		t.Fatalf("dirty: %q", st)
	}
	// Synthetic issue: validated against |issue|, git untouched.
	syn := req(5, "issue.md", "x")
	syn.Issue = -5
	res, err = c.Commit(context.Background(), syn)
	if err != nil || !res.Synthetic {
		t.Fatalf("%+v, %v", res, err)
	}
	bad := req(6, "x", "x")
	bad.Issue = -5
	if _, err := c.Commit(context.Background(), bad); !errors.Is(err, ErrGuard) {
		t.Fatalf("err = %v", err)
	}
}

func TestIdentityRequiredLive(t *testing.T) {
	e := newEnv(t)
	e.cfg.Bot = github.Identity{}
	if _, err := NewMain(context.Background(), e.cfg, e.live(), e.g); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestCloseAndRecovery(t *testing.T) {
	e := newEnv(t)
	// A dirty leftover directory is replaced.
	left := filepath.Join(e.work, "committer-main")
	_ = os.MkdirAll(left, 0o755)
	_ = os.WriteFile(filepath.Join(left, "junk"), []byte("x"), 0o644)
	c, err := NewMain(context.Background(), e.cfg, e.live(), e.g)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(left, "junk")); err == nil {
		t.Fatal("junk survived")
	}
	// Cancelled context: nothing runs.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.l.sem <- struct{}{} // hold the lane so the request must wait
	if _, err := c.Commit(ctx, req(1, "a", "a")); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	<-c.l.sem
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(left); err == nil {
		t.Fatal("worktree survived Close")
	}
	if _, err := c.Commit(context.Background(), req(1, "a", "a")); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
}

func TestBranches(t *testing.T) {
	e := newEnv(t)
	gittest.Run(t, e.clone, "push", "-q", "origin", "main:refs/heads/worker/eng-a", "main:refs/heads/worker/eng-b")
	b := NewBranches(e.cfg, e.live(), e.g)
	defer func() { _ = b.Close(context.Background()) }()
	ctx := context.Background()

	for _, br := range []string{"main", "feature/x", "worker/", "worker/A"} {
		if _, err := b.Commit(ctx, br, req(1, "a", "a")); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", br, err)
		}
	}
	if _, err := b.Commit(ctx, "worker/eng-missing", req(1, "a", "a")); !errors.Is(err, ErrBranchGone) {
		t.Fatalf("missing: %v", err)
	}
	var wg sync.WaitGroup
	for i := 1; i <= 6; i++ {
		br := []string{"worker/eng-a", "worker/eng-b"}[i%2]
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := b.Commit(ctx, br, req(i, "a", "a")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for _, br := range []string{"worker/eng-a", "worker/eng-b"} {
		if n := strings.TrimSpace(gittest.Run(t, e.bare, "rev-list", "--count", br+"..."+br)); n != "0" {
			t.Fatal(n)
		}
		if n := strings.TrimSpace(gittest.Run(t, e.bare, "rev-list", "--count", "main.."+br)); n != "3" {
			t.Fatalf("%s ahead by %s", br, n)
		}
	}
	if e.head(t, "main") != strings.TrimSpace(gittest.Run(t, e.clone, "rev-parse", "origin/main")) {
		t.Fatal("main moved")
	}
	// The branch is deleted: a push must not recreate it.
	gittest.Run(t, e.bare, "update-ref", "-d", "refs/heads/worker/eng-a")
	if _, err := b.Commit(ctx, "worker/eng-a", req(1, "z", "z")); !errors.Is(err, ErrBranchGone) {
		t.Fatalf("deleted: %v", err)
	}
	if err := b.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Commit(ctx, "worker/eng-b", req(1, "a", "a")); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestRemoteURLValidation(t *testing.T) {
	tok := func(context.Context) (string, error) { return "t", nil }
	tests := []struct {
		url   string
		token gitx.TokenFunc
		ok    bool
	}{
		{"https://github.com/o/r.git", tok, true},
		{"file:///tmp/x.git", nil, true},
		{"file:///tmp/x.git", tok, false},
		{"http://github.com/o/r.git", nil, false},
		{"ssh://git@github.com/o/r.git", nil, false},
		{"git@github.com:o/r.git", nil, false},
		{"/tmp/x.git", nil, false},
	}
	for _, tc := range tests {
		e := newEnv(t)
		e.cfg.RemoteURL, e.cfg.Token = tc.url, tc.token
		_, err := newCore(e.cfg, e.live(), e.g, true)
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrInvalid)) {
			t.Errorf("%s token=%v: err = %v", tc.url, tc.token != nil, err)
		}
	}
}

func TestSkipCIDefaults(t *testing.T) {
	e := newEnv(t)
	m, err := newCore(e.cfg, e.live(), e.g, true)
	b, err2 := newCore(e.cfg, e.live(), e.g, false)
	if err != nil || err2 != nil || !m.skipCI || b.skipCI {
		t.Fatalf("main %v branch %v (%v %v)", m.skipCI, b.skipCI, err, err2)
	}
	on := true
	e.cfg.SkipCI = &on
	if b, _ := newCore(e.cfg, e.live(), e.g, false); !b.skipCI {
		t.Fatal("explicit SkipCI ignored")
	}
}

func TestSymlinkedParents(t *testing.T) {
	tests := []struct {
		name, link, target, escaped string // escaped: path relative to the worktree's parent that must stay absent
	}{
		{"dir", ".ard/12", "../.github", ""},
		{"file", ".ard/12/issue.md", "../../../outside.txt", "outside.txt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			full := filepath.Join(e.clone, filepath.FromSlash(tc.link))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(tc.target, full); err != nil {
				t.Fatal(err)
			}
			gittest.Run(t, e.clone, "add", "-A")
			gittest.Run(t, e.clone, "commit", "-q", "-m", "evil symlink")
			gittest.Run(t, e.clone, "push", "-q", "origin", "main")
			before := e.head(t, "main")
			c := e.main(t, e.live())
			_, err := c.Commit(context.Background(), req(12, "issue.md", "secret"))
			if !errors.Is(err, ErrGuard) {
				t.Fatalf("err = %v", err)
			}
			if e.head(t, "main") != before {
				t.Fatal("remote moved")
			}
			for _, p := range []string{filepath.Join(e.work, "outside.txt"), filepath.Join(c.l.wt, ".github", "issue.md")} {
				if _, err := os.Stat(p); err == nil {
					t.Fatalf("%s was written", p)
				}
			}
		})
	}
}

func onlyClone(t *testing.T, dir string) {
	t.Helper()
	n := 0
	for _, l := range strings.Split(gittest.Run(t, dir, "worktree", "list", "--porcelain"), "\n") {
		if strings.HasPrefix(l, "worktree ") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d worktrees left, want only the clone:\n%s", n, gittest.Run(t, dir, "worktree", "list"))
	}
}

// TestBranchesStress runs several branch lanes and the main lane at once. It
// guards the shared worktree-administration lock (#109).
func TestBranchesStress(t *testing.T) {
	const nBranches, m = 4, 3
	e := newEnv(t)
	base := e.head(t, "main")
	var refs []string
	var names []string
	for i := 0; i < nBranches; i++ {
		br := fmt.Sprintf("worker/eng-%d", i)
		names = append(names, br)
		refs = append(refs, "main:refs/heads/"+br)
	}
	gittest.Run(t, e.clone, append([]string{"push", "-q", "origin"}, refs...)...)
	live := e.live()
	b := NewBranches(e.cfg, live, e.g)
	mainLane := e.main(t, live)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i, br := range names {
		for j := 0; j < m; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := b.Commit(ctx, br, req(100+i*10+j, "a", "a")); err != nil {
					t.Errorf("%s: %v", br, err)
				}
			}()
		}
	}
	for j := 0; j < m; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := mainLane.Commit(ctx, req(900+j, "a", "a")); err != nil {
				t.Errorf("main: %v", err)
			}
		}()
	}
	wg.Wait()
	for _, br := range names {
		if n := strings.TrimSpace(gittest.Run(t, e.bare, "rev-list", "--count", base+".."+br)); n != fmt.Sprint(m) {
			t.Errorf("%s ahead by %s, want %d", br, n, m)
		}
	}
	if err := b.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := mainLane.Close(ctx); err != nil {
		t.Fatal(err)
	}
	onlyClone(t, e.clone)
}

// TestWorktreeAdminSharedWithExecutor checks that branch lanes and the local
// executor share one worktree lock on the same RepoDir.
func TestWorktreeAdminSharedWithExecutor(t *testing.T) {
	e := newEnv(t)
	gittest.Run(t, e.clone, "push", "-q", "origin", "main:refs/heads/worker/eng-0", "main:refs/heads/worker/eng-1")
	b := NewBranches(e.cfg, e.live(), e.g)
	defer func() { _ = b.Close(context.Background()) }()
	lx, err := local.New(local.Options{Root: t.TempDir(), Log: e.cfg.Log})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		for j := 0; j < 3; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := b.Commit(ctx, fmt.Sprintf("worker/eng-%d", i), req(10*i+j+1, "a", "a")); err != nil {
					t.Error(err)
				}
			}()
		}
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				h, err := lx.Prepare(ctx, executor.Job{ID: fmt.Sprintf("C%dX%d", g, i), Repo: "o/r", Issue: 100 + g*10 + i, Stage: "scanner", Role: "s",
					RepoDir: e.clone, Ref: "HEAD", RoleDir: t.TempDir(), Timeout: time.Minute})
				if err != nil {
					t.Errorf("prepare: %v", err)
					return
				}
				if err := lx.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
					t.Errorf("cleanup: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := b.Close(ctx); err != nil {
		t.Fatal(err)
	}
	onlyClone(t, e.clone)
}

func TestCommitMergeSeesLatestHead(t *testing.T) {
	e := newEnv(t)
	c := e.main(t, e.live())
	if _, err := c.Commit(context.Background(), req(12, "plan.md", "head")); err != nil {
		t.Fatal(err)
	}
	r := req(12, "plan.md", "mine")
	r.Files[0].Merge = func(cur []byte, exists bool, mine []byte) ([]byte, error) {
		if !exists || string(cur) != "head" {
			t.Errorf("cur = %q exists %v", cur, exists)
		}
		return append(append([]byte{}, cur...), mine...), nil
	}
	if _, err := c.Commit(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if out := gittest.Run(t, e.bare, "show", "main:.ard/12/plan.md"); out != "headmine" {
		t.Fatalf("content %q", out)
	}
}

func TestCommitMergeError(t *testing.T) {
	e := newEnv(t)
	c := e.main(t, e.live())
	before := e.head(t, "main")
	r := req(12, "plan.md", "mine")
	r.Files[0].Merge = func([]byte, bool, []byte) ([]byte, error) { return nil, errors.New("no") }
	if _, err := c.Commit(context.Background(), r); !errors.Is(err, ErrMerge) {
		t.Fatalf("err = %v", err)
	}
	if e.head(t, "main") != before {
		t.Fatal("main moved")
	}
}
