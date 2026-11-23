//go:build unix

package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
)

func gitEnv(c *exec.Cmd) {
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x", "GIT_CONFIG_GLOBAL=/dev/null")
}

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command(args[0], args[1:]...)
	c.Dir = dir
	gitEnv(c)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type fixture struct {
	l       *Local
	opts    Options
	repo    string
	roleDir string
	root    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available")
	}
	repo := t.TempDir()
	sh(t, repo, "git", "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644)
	sh(t, repo, "git", "add", ".")
	sh(t, repo, "git", "commit", "-q", "-m", "init")
	root := t.TempDir()
	opts := Options{Root: root, KillGrace: 200 * time.Millisecond, PathEnv: "/usr/bin:/bin", AllowEnv: []string{"ANTHROPIC_API_KEY"}}
	l, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{l: l, opts: opts, repo: repo, roleDir: t.TempDir(), root: root}
}

var jobSeq int

func (f *fixture) job(timeout time.Duration) executor.Job {
	jobSeq++
	return executor.Job{ID: "JOB" + strconv.Itoa(jobSeq), Repo: "o/r", Issue: 7, Stage: "worker", Role: "dev",
		RepoDir: f.repo, Ref: "HEAD", RoleDir: f.roleDir, Timeout: timeout}
}

func agent(t *testing.T, mode string, env ...string) executor.Command {
	p, err := filepath.Abs("testdata/fakeagent.sh")
	if err != nil {
		t.Fatal(err)
	}
	return executor.Command{Path: "/bin/sh", Args: []string{p, mode}, Env: env}
}

func (f *fixture) run(t *testing.T, j executor.Job, c executor.Command) (executor.Handle, executor.Status) {
	t.Helper()
	ctx := context.Background()
	h, err := f.l.Prepare(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.l.Cleanup(context.Background(), h, executor.CleanupOptions{}) })
	h, err = f.l.Start(ctx, h, c)
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	st, err := executor.WaitFor(wctx, f.l, h, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	return h, st
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSuccessAndLogs(t *testing.T) {
	f := newFixture(t)
	j := f.job(time.Minute)
	j.Inputs = []executor.File{{Path: ".ard/7/issue.md", Data: []byte("UNTRUSTED\n")}}
	h, st := f.run(t, j, agent(t, "ok"))
	if st.State != executor.Exited || st.ExitCode != 0 {
		t.Fatalf("status %+v", st)
	}
	if got := readFile(t, h.StdoutPath); got != "out\n" {
		t.Errorf("stdout file %q", got)
	}
	if all := readFile(t, h.LogPath); !strings.Contains(all, "out") || !strings.Contains(all, "err") {
		t.Errorf("combined log %q", all)
	}
	if readFile(t, filepath.Join(h.Worktree, "result.txt")) != "done\n" {
		t.Error("result.txt missing")
	}
	if readFile(t, filepath.Join(h.Worktree, ".ard/7/issue.md")) != "UNTRUSTED\n" {
		t.Error("input not written")
	}
	if !strings.Contains(h.LogPath, "/logs/o/r/7/worker-dev-") {
		t.Errorf("log path %s", h.LogPath)
	}
	r, err := f.l.Logs(context.Background(), h, executor.LogsOptions{Stream: executor.Stdout})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	r.Close()
	if string(b) != "out\n" {
		t.Errorf("Logs stdout %q", b)
	}
}

func TestFailAndCrash(t *testing.T) {
	tests := []struct {
		mode   string
		code   int
		signal string
	}{{"fail", 3, ""}, {"crash", -1, "killed"}}
	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			f := newFixture(t)
			_, st := f.run(t, f.job(time.Minute), agent(t, tc.mode))
			if st.State != executor.Exited || st.ExitCode != tc.code || st.Signal != tc.signal {
				t.Fatalf("status %+v", st)
			}
		})
	}
}

func TestTimeoutKillsGroup(t *testing.T) {
	for _, mode := range []string{"hang", "stubborn"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			h, st := f.run(t, f.job(time.Second), agent(t, mode))
			if st.State != executor.TimedOut {
				t.Fatalf("status %+v", st)
			}
			if alive(h.PID) {
				t.Error("agent still alive")
			}
			if mode == "hang" {
				pid, _ := strconv.Atoi(strings.TrimSpace(readFile(t, filepath.Join(filepath.Dir(h.Worktree), "grandchild."+h.JobID))))
				time.Sleep(100 * time.Millisecond)
				if pid == 0 || alive(pid) {
					t.Errorf("grandchild %d alive", pid)
				}
			}
		})
	}
}

func TestEnvScrubbed(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "leak")
	t.Setenv("GH_TOKEN", "leak")
	t.Setenv("SSH_AUTH_SOCK", "/leak")
	f := newFixture(t)
	h, st := f.run(t, f.job(time.Minute), agent(t, "env", "ANTHROPIC_API_KEY=sk-test"))
	if st.State != executor.Exited {
		t.Fatalf("%+v", st)
	}
	out := readFile(t, h.StdoutPath)
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		k, v, _ := strings.Cut(line, "=")
		got[k] = v
	}
	for _, ignore := range []string{"PWD", "SHLVL", "_", "OLDPWD"} {
		delete(got, ignore)
	}
	want := []string{"PATH", "HOME", "LANG", "GOFLAGS", "TMPDIR", "GIT_TERMINAL_PROMPT", "GIT_CONFIG_NOSYSTEM", "GIT_CONFIG_GLOBAL",
		"ARD_JOB", "ARD_ISSUE", "ARD_STAGE", "ARD_ROLE", "ARD_WORK", "ARD_ROLE_DIR", "ANTHROPIC_API_KEY"}
	if len(got) != len(want) {
		t.Errorf("env names %v", got)
	}
	for _, n := range want {
		if _, ok := got[n]; !ok {
			t.Errorf("missing %s", n)
		}
	}
	if got["GOFLAGS"] != "-modcacherw" {
		t.Errorf("GOFLAGS %q, want -modcacherw", got["GOFLAGS"])
	}
	if got["HOME"] != h.Home {
		t.Errorf("HOME %q", got["HOME"])
	}
	// The handle never carries secrets.
	b, _ := json.Marshal(h)
	if strings.Contains(string(b), "sk-test") {
		t.Error("handle contains secret")
	}
	var back executor.Handle
	if err := json.Unmarshal(b, &back); err != nil || back.JobID != h.JobID || back.StdoutPath != h.StdoutPath {
		t.Errorf("round trip: %v %+v", err, back)
	}
}

func TestForbiddenEnv(t *testing.T) {
	f := newFixture(t)
	for _, e := range []string{"GITHUB_TOKEN=x", "GH_TOKEN=x", "HOME=/x", "PATH=/x", "ARD_X=1", "SSH_AUTH_SOCK=/x", "FOO", "OTHER=1"} {
		if err := f.l.checkEnv([]string{e}); !errors.Is(err, executor.ErrForbiddenEnv) {
			t.Errorf("%q: %v", e, err)
		}
	}
	if err := f.l.checkEnv([]string{"ANTHROPIC_API_KEY=k"}); err != nil {
		t.Error(err)
	}
}

func TestFixedEnv(t *testing.T) {
	f := newFixture(t)
	for _, e := range []string{"GITHUB_TOKEN=x", "HOME=/x", "ARD_X=1", "MY_API_KEY=x", "FOO", "ANTHROPIC_BASE_URL=x", "HTTPS_PROXY=x", "NODE_OPTIONS=x"} {
		if err := f.l.checkFixed([]string{e}); !errors.Is(err, executor.ErrForbiddenEnv) {
			t.Errorf("%q: %v", e, err)
		}
	}
	if err := f.l.checkFixed([]string{"CLAUDE_CONFIG_DIR=/cfg", "DISABLE_AUTOUPDATER=1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1"}); err != nil {
		t.Error(err)
	}
}

func TestFixedReachesProcess(t *testing.T) {
	f := newFixture(t)
	f.l.allowFixed["A"] = true
	c := agent(t, "env")
	c.Fixed = []string{"A=1"}
	h, _ := f.run(t, f.job(20*time.Second), c)
	if out := readFile(t, h.StdoutPath); !strings.Contains("\n"+out, "\nA=1\n") {
		t.Errorf("A=1 missing from env:\n%s", out)
	}
}

func TestLastOutputAdvances(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h, err := f.l.Prepare(ctx, f.job(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.l.Cleanup(ctx, h, executor.CleanupOptions{}) })
	if st, _ := f.l.Status(ctx, h); st.State != executor.Prepared {
		t.Fatalf("want prepared, got %+v", st)
	}
	h, err = f.l.Start(ctx, h, agent(t, "talk"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.l.Start(ctx, h, agent(t, "talk")); !errors.Is(err, executor.ErrAlreadyStarted) {
		t.Errorf("second start: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	a, _ := f.l.Status(ctx, h)
	time.Sleep(300 * time.Millisecond)
	b, _ := f.l.Status(ctx, h)
	if a.State != executor.Running || !b.LastOutput.After(a.LastOutput) {
		t.Errorf("a=%+v b=%+v", a, b)
	}
	f.l.Cleanup(ctx, h, executor.CleanupOptions{})
}

func TestStartCtxDoesNotBoundJob(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	h, err := f.l.Prepare(ctx, f.job(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.l.Cleanup(context.Background(), h, executor.CleanupOptions{}) })
	h, err = f.l.Start(ctx, h, agent(t, "talk"))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(200 * time.Millisecond)
	if st, _ := f.l.Status(context.Background(), h); st.State != executor.Running {
		t.Fatalf("job stopped: %+v", st)
	}
}

func TestRestartOrphanAndPIDReuse(t *testing.T) {
	if procStart(os.Getpid()) == "" {
		t.Skip("ps unavailable (sandbox?)")
	}
	f := newFixture(t)
	ctx := context.Background()
	h, err := f.l.Prepare(ctx, f.job(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	h, err = f.l.Start(ctx, h, agent(t, "hang"))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := New(f.opts)
	time.Sleep(200 * time.Millisecond)
	if st, _ := second.Status(ctx, h); st.State != executor.Orphaned {
		t.Fatalf("want orphaned, got %+v", st)
	}
	// A stale ProcStart means PID reuse: gone, and never signalled.
	stale := h
	stale.ProcStart = "Mon Jan  1 00:00:00 2001"
	if st, _ := second.Status(ctx, stale); st.State != executor.Gone {
		t.Fatalf("want gone, got %+v", st)
	}
	second.Cleanup(ctx, stale, executor.CleanupOptions{KeepWorktree: true})
	if !alive(h.PID) {
		t.Fatal("unverified PID was signalled")
	}
	if err := second.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if st, _ := second.Status(ctx, h); st.State != executor.Gone {
		t.Fatalf("want gone after cleanup, got %+v", st)
	}
	f.l.Cleanup(ctx, h, executor.CleanupOptions{})
}

func TestCleanup(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h, _ := f.run(t, f.job(time.Minute), agent(t, "ok"))
	if err := f.l.Cleanup(ctx, h, executor.CleanupOptions{KeepWorktree: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.Worktree); err != nil {
		t.Fatal("KeepWorktree removed it")
	}
	for i := 0; i < 2; i++ { // second call is a no-op
		if err := f.l.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(h.Worktree); !os.IsNotExist(err) {
		t.Error("worktree remains")
	}
	if list := sh(t, f.repo, "git", "worktree", "list"); strings.Contains(list, h.Worktree) {
		t.Errorf("worktree still registered: %s", list)
	}
	if _, err := os.Stat(h.LogPath); err != nil {
		t.Error("log was deleted")
	}
}

func TestPrepareErrors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	j := f.job(time.Minute)
	j.Ref = "nope"
	if _, err := f.l.Prepare(ctx, j); err == nil {
		t.Error("bad ref accepted")
	}
	if _, err := os.Stat(mustWT(t, f.l, j)); !os.IsNotExist(err) {
		t.Error("worktree left behind")
	}
	j = f.job(time.Minute)
	j.Inputs = []executor.File{{Path: "a.txt/sub", Data: []byte("x")}} // a.txt is a file: write fails
	if _, err := f.l.Prepare(ctx, j); err == nil {
		t.Fatal("input failure not reported")
	}
	if _, err := os.Stat(mustWT(t, f.l, j)); !os.IsNotExist(err) {
		t.Error("worktree left after rollback")
	}
	if list := sh(t, f.repo, "git", "worktree", "list"); strings.Count(list, "\n") != 0 {
		t.Errorf("registered worktree left: %s", list)
	}
	j = f.job(time.Minute)
	h, err := f.l.Prepare(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	defer f.l.Cleanup(ctx, h, executor.CleanupOptions{})
	if _, err := f.l.Prepare(ctx, j); !errors.Is(err, executor.ErrExists) {
		t.Errorf("duplicate: %v", err)
	}
}

func TestReadOnlyHost(t *testing.T) {
	f := newFixture(t)
	j := f.job(time.Minute)
	j.ReadOnly = true
	h, st := f.run(t, j, agent(t, "write"))
	if os.Getuid() != 0 && st.ExitCode == 0 {
		t.Error("write into read-only worktree succeeded")
	}
	if _, err := os.Stat(filepath.Join(h.Worktree, "probe.txt")); err == nil && os.Getuid() != 0 {
		t.Error("probe.txt exists")
	}
	if h.StdoutPath == "" {
		t.Error("StdoutPath empty")
	}
	// Cleanup must still remove a read-only worktree.
	if err := f.l.Cleanup(context.Background(), h, executor.CleanupOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.Worktree); !os.IsNotExist(err) {
		t.Error("read-only worktree not removed")
	}
}

func TestPrune(t *testing.T) {
	f := newFixture(t)
	h, _ := f.run(t, f.job(time.Minute), agent(t, "ok"))
	old := time.Now().Add(-8 * 24 * time.Hour)
	os.Chtimes(h.LogPath, old, old)
	if err := f.l.Prune(context.Background(), 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.LogPath); !os.IsNotExist(err) {
		t.Error("old log kept")
	}
	if _, err := os.Stat(h.StdoutPath); err != nil {
		t.Error("fresh log removed")
	}
}

func TestPruneRemovesEmptyNestedDirs(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.l.root, "logs", "o", "r", "7")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "old.log")
	os.WriteFile(p, []byte("x"), 0o644)
	old := time.Now().Add(-8 * 24 * time.Hour)
	os.Chtimes(p, old, old)
	if err := f.l.Prune(context.Background(), 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.l.root, "logs", "o")); !os.IsNotExist(err) {
		t.Error("empty owner dir kept")
	}
	if _, err := os.Stat(filepath.Join(f.l.root, "logs")); err != nil {
		t.Error("logs dir removed")
	}
}

func TestGitConfigInRepoNotExecuted(t *testing.T) {
	f := newFixture(t)
	marker := filepath.Join(t.TempDir(), "marker")
	hook := filepath.Join(f.repo, "fsm.sh")
	os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)
	os.WriteFile(filepath.Join(f.repo, ".gitconfig"), []byte("[core]\n\tfsmonitor = "+hook+"\n"), 0o644)
	os.MkdirAll(filepath.Join(f.repo, ".git/hooks"), 0o755)
	if err := os.WriteFile(filepath.Join(f.repo, ".git/hooks/post-checkout"), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sh(t, f.repo, "git", "add", ".")
	sh(t, f.repo, "git", "commit", "-q", "-m", "evil")
	h, err := f.l.Prepare(context.Background(), f.job(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	defer f.l.Cleanup(context.Background(), h, executor.CleanupOptions{})
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("repo-controlled config or hook executed")
	}
}

func TestInputSymlinkRejected(t *testing.T) {
	f := newFixture(t)
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("safe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(f.repo, ".ard/7"), 0o755)
	os.Symlink(target, filepath.Join(f.repo, ".ard/7/issue.md"))
	sh(t, f.repo, "git", "add", ".")
	sh(t, f.repo, "git", "commit", "-q", "-m", "link")
	j := f.job(time.Minute)
	j.Inputs = []executor.File{{Path: ".ard/7/issue.md", Data: []byte("UNTRUSTED")}}
	if _, err := f.l.Prepare(context.Background(), j); err == nil {
		t.Fatal("symlinked input accepted")
	}
	if readFile(t, target) != "safe\n" {
		t.Error("symlink target was written")
	}
	if _, err := os.Stat(mustWT(t, f.l, j)); !os.IsNotExist(err) {
		t.Error("worktree left behind")
	}
}

func TestCleanupPreparedHandleDoesNotSignalSelf(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h, err := f.l.Prepare(ctx, f.job(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.l.Start(ctx, h, agent(t, "hang"))
	if err != nil {
		t.Fatal(err)
	}
	// h still has PID 0; the tracker must supply the real pid.
	if err := f.l.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
		t.Fatal(err)
	}
	if alive(started.PID) {
		t.Error("tracked job not stopped")
	}
	f.l.stop(h, nil) // PID 0 must be a no-op (the test process survives)
}

func mustWT(t *testing.T, l *Local, j executor.Job) string {
	t.Helper()
	p, err := l.WorktreePath(j.Repo, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestPrepareCleanupConcurrent runs many Prepare+Cleanup cycles on one repo
// to exercise the shared worktree lock (#109). Jobs are built inline: f.job
// uses an unsynchronised counter.
func TestPrepareCleanupConcurrent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 4; i++ {
				h, err := f.l.Prepare(ctx, executor.Job{ID: fmt.Sprintf("CC%dX%d", g, i), Repo: "o/r", Issue: 100 + g*10 + i, Stage: "worker", Role: "dev",
					RepoDir: f.repo, Ref: "HEAD", RoleDir: f.roleDir, Timeout: time.Minute})
				if err != nil {
					t.Errorf("prepare: %v", err)
					return
				}
				if err := f.l.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
					t.Errorf("cleanup: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	n := 0
	for _, l := range strings.Split(sh(t, f.repo, "git", "worktree", "list", "--porcelain"), "\n") {
		if strings.HasPrefix(l, "worktree ") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d worktrees left, want 1", n)
	}
}

func TestCleanupRunsWhenCancelled(t *testing.T) {
	f := newFixture(t)
	h, err := f.l.Prepare(context.Background(), executor.Job{ID: "CANCEL1", Repo: "o/r", Issue: 9, Stage: "worker", Role: "dev",
		RepoDir: f.repo, Ref: "HEAD", RoleDir: f.roleDir, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.l.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.Worktree); !os.IsNotExist(err) {
		t.Errorf("worktree still there: %v", err)
	}
	n := 0
	for _, l := range strings.Split(sh(t, f.repo, "git", "worktree", "list", "--porcelain"), "\n") {
		if strings.HasPrefix(l, "worktree ") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d worktrees, want 1", n)
	}
}

func TestScrubRemovesCommittedFileBeforeStart(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(filepath.Join(f.repo, ".ard/7"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, ".ard/7/worker-dev.md"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, f.repo, "git", "add", ".")
	sh(t, f.repo, "git", "commit", "-q", "-m", "role file")
	tests := []struct {
		name   string
		scrub  []string
		exists bool
	}{
		{"scrubbed", []string{".ard/7/worker-dev.md"}, false},
		{"missing path is fine", []string{".ard/7/worker-other.md"}, true},
		{"no scrub keeps it", nil, true},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			j := f.job(time.Minute)
			j.Scrub = c.scrub
			h, err := f.l.Prepare(context.Background(), j)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { f.l.Cleanup(context.Background(), h, executor.CleanupOptions{}) })
			if len(h.Scrubbed) != len(c.scrub) {
				t.Errorf("Scrubbed = %v, want %v", h.Scrubbed, c.scrub)
			}
			_, err = os.Lstat(filepath.Join(h.Worktree, ".ard/7/worker-dev.md"))
			if (err == nil) != c.exists {
				t.Errorf("exists = %v, want %v (%v)", err == nil, c.exists, err)
			}
		})
	}
}

func TestScrubGuards(t *testing.T) {
	const rel = ".ard/7/worker-dev.md"
	tests := []struct {
		name  string
		setup func(t *testing.T, repo string)
		fail  bool
	}{
		{"parent is a symlink", func(t *testing.T, repo string) {
			if err := os.MkdirAll(filepath.Join(repo, "elsewhere"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, "elsewhere/worker-dev.md"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(repo, ".ard"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../elsewhere", filepath.Join(repo, ".ard/7")); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"target is a directory", func(t *testing.T, repo string) {
			if err := os.MkdirAll(filepath.Join(repo, rel), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, rel, "keep"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"target is a symlink: link removed", func(t *testing.T, repo string) {
			if err := os.MkdirAll(filepath.Join(repo, ".ard/7"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../../a.txt", filepath.Join(repo, rel)); err != nil {
				t.Fatal(err)
			}
		}, false},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			wt := t.TempDir()
			c.setup(t, wt)
			err := scrub(wt, []string{rel})
			if (err != nil) != c.fail {
				t.Fatalf("err = %v, want failure %v", err, c.fail)
			}
			if !c.fail {
				if _, err := os.Lstat(filepath.Join(wt, rel)); err == nil {
					t.Error("link still there")
				}
			}
			if c.name == "parent is a symlink" {
				if _, err := os.Stat(filepath.Join(wt, "elsewhere/worker-dev.md")); err != nil {
					t.Error("file behind the symlink was removed")
				}
			}
		})
	}
}

type failingAdd struct{ gitRunner }

func (failingAdd) WorktreeAdd(context.Context, string, string, string) error {
	return errors.New("fatal: could not create work tree")
}

// A failed ref lookup or worktree add is a transient clone failure
// (executor.ErrClone); a bad job is not.
func TestPrepareWrapsErrClone(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	j := f.job(time.Minute)
	j.Ref = "nope"
	if _, err := f.l.Prepare(ctx, j); !errors.Is(err, executor.ErrClone) {
		t.Errorf("bad ref: %v, want ErrClone", err)
	}
	f = newFixture(t)
	f.l.git = failingAdd{f.l.git}
	if _, err := f.l.Prepare(ctx, f.job(time.Minute)); !errors.Is(err, executor.ErrClone) {
		t.Errorf("worktree add: %v, want ErrClone", err)
	}
	bad := f.job(time.Minute)
	bad.Timeout = 0
	if _, err := f.l.Prepare(ctx, bad); errors.Is(err, executor.ErrClone) || !errors.Is(err, executor.ErrInvalidJob) {
		t.Errorf("invalid job: %v", err)
	}
}

// A handle stored before #113 names the operator's checkout as RepoDir. With
// CloneDir set, Cleanup removes its worktree without running git there.
func TestCleanupForeignRepoDir(t *testing.T) {
	for _, tc := range []struct {
		name          string
		cloneIsRepo   bool // CloneDir == the job's RepoDir
		cloneDirUnset bool
		wantGit       bool // git worktree admin ran in RepoDir
	}{
		{"foreign", false, false, false},
		{"own", true, false, true},
		{"unset", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			opts := f.opts
			opts.CloneDir = f.repo
			if !tc.cloneIsRepo {
				opts.CloneDir = t.TempDir()
			}
			if tc.cloneDirUnset {
				opts.CloneDir = ""
			}
			l, err := New(opts)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			h, err := l.Prepare(ctx, f.job(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(sh(t, f.repo, "git", "worktree", "list"), h.Worktree) {
				t.Fatal("worktree not registered")
			}
			adminBefore := sh(t, f.repo, "ls", ".git/worktrees")
			if err := l.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(h.Worktree); !os.IsNotExist(err) {
				t.Error("worktree remains")
			}
			registered := strings.Contains(sh(t, f.repo, "git", "worktree", "list"), h.Worktree)
			if tc.wantGit == registered {
				t.Errorf("registered after cleanup = %v, want %v", registered, !tc.wantGit)
			}
			if !tc.wantGit {
				if after := sh(t, f.repo, "ls", ".git/worktrees"); after != adminBefore {
					t.Errorf("checkout's worktree admin changed: %q -> %q", adminBefore, after)
				}
			}
		})
	}
}
