//go:build unix

package kube

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/kube/api"
	"github.com/albertcbrown/ard/internal/jobpod"
)

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLifecycle(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	h, err := e.ex.Prepare(ctx, testJob("01HAAAAAAAAAAAAAAAAAAAAAAA"))
	if err != nil {
		t.Fatal(err)
	}
	if h.Kind != Kind || h.Container != jobpod.PodName(h.JobID) || h.View.Work != jobpod.MountWork || h.Image != "agent@sha256:aa" {
		t.Errorf("handle %+v", h)
	}
	if st, _ := e.ex.Status(ctx, h); st.State != executor.Prepared {
		t.Errorf("before start: %+v", st)
	}
	if h, err = e.ex.Start(ctx, h, testCmd()); err != nil {
		t.Fatal(err)
	}
	if want := h.StartedAt.Add(5*time.Minute + 20*time.Minute); !h.Deadline.Equal(want) {
		t.Errorf("deadline %v, want %v", h.Deadline, want)
	}
	if len(e.tok.minted) != 1 || len(e.tok.minted[0]) != 1 || e.tok.minted[0][0].Name != "r" {
		t.Errorf("minted %+v", e.tok.minted)
	}
	if st, _ := e.ex.Status(ctx, h); st.State != executor.Running || !st.Alive {
		t.Errorf("running: %+v", st)
	}
	sec, ok := e.fake.Secret(ns, jobpod.PodName(h.JobID))
	if !ok || string(sec.Data[jobpod.ItemGitAccess]) != "tok-b" || sec.Meta.OwnerReferences[0].UID == "" {
		t.Fatalf("secret %+v", sec)
	}
	if got := e.ex.Secrets(); len(got) != 1 || got[0] != "tok-b" {
		t.Errorf("Secrets() = %v", got)
	}

	e.preDone(h, 0, "")
	deadline := time.Now().Add(3 * time.Second)
	for e.tok.nRevoked() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	e.fake.SetLogs(ns, jobpod.PodName(h.JobID), "agent", []string{"line one", "line two"})
	e.serve(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+e.key(h) || r.URL.Path != jobpod.ResultPath {
			http.Error(w, "no", 401)
			return
		}
		w.Write(okResult(t))
	})
	e.agentDone(h, 3, 0)
	st := e.waitTerminal(h)
	if st.State != executor.Exited || st.ExitCode != 3 {
		t.Fatalf("status %+v", st)
	}
	if got := e.tok.nRevoked(); got != 1 {
		t.Errorf("token revoked %d times, want 1", got)
	}
	if len(e.ex.Secrets()) != 0 {
		t.Errorf("Secrets() after revoke = %v", e.ex.Secrets())
	}
	if readFile(t, filepath.Join(h.Worktree, "out.txt")) != "result" {
		t.Error("worktree not swapped")
	}
	if readFile(t, filepath.Join(h.Worktree, ".git")) != "gitdir: /repo/.git/worktrees/x\n" {
		t.Error("the result's own .git was not replaced by the clone link")
	}
	if _, err := os.Stat(h.Worktree + ".input"); err != nil {
		t.Error("original worktree not kept as .input")
	}
	if readFile(t, h.StdoutPath) != "agent said hi\n" {
		t.Errorf("stdout = %q", readFile(t, h.StdoutPath))
	}
	if !strings.Contains(strings.Join(e.rt.seen, ","), podIP+":8080") {
		t.Errorf("pulled from %v", e.rt.seen)
	}
	// The log follower catches up after the agent ended.
	deadline = time.Now().Add(3 * time.Second)
	for !strings.Contains(readFile(t, h.LogPath), "line two") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := readFile(t, h.LogPath); got != "line one\nline two\n" {
		t.Errorf("log = %q", got)
	}
	rc, err := e.ex.Logs(ctx, h, executor.LogsOptions{TailLines: 1})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(rc); string(b) != "line two\n" {
		t.Errorf("tail = %q", b)
	}
	if err := e.ex.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := e.tok.nRevoked(); got != 1 {
		t.Errorf("cleanup revoked again: %d", got)
	}
}

func TestPull(t *testing.T) {
	for _, tc := range []struct {
		name     string
		handler  func(e *env, hits *atomic.Int32) http.HandlerFunc
		recreate bool
		state    executor.State
		end      executor.End
		minHits  int32
		maxHits  int32
	}{
		{"409 twice then 200", func(e *env, hits *atomic.Int32) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				if hits.Add(1) <= 2 {
					http.Error(w, "busy", http.StatusConflict)
					return
				}
				w.Write(okResult(t))
			}
		}, false, executor.Exited, "", 3, 3},
		{"unsafe tar", func(e *env, hits *atomic.Int32) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Write(tarGz(t, map[string]string{"work/../../escape": "x"}))
			}
		}, false, executor.Gone, executor.EndBadResult, 1, 1},
		{"git entry", func(e *env, hits *atomic.Int32) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Write(tarGz(t, map[string]string{"work/.git": "evil"}))
			}
		}, false, executor.Gone, executor.EndBadResult, 1, 1},
		{"no work tree", func(e *env, hits *atomic.Int32) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Write(tarGz(t, map[string]string{"out/stdout": "x"}))
			}
		}, false, executor.Gone, executor.EndBadResult, 1, 1},
		{"deadline passes", func(e *env, hits *atomic.Int32) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				if hits.Add(1) == 2 {
					e.advance(time.Hour)
				}
				http.Error(w, "down", http.StatusServiceUnavailable)
			}
		}, false, executor.Gone, executor.EndNoResult, 2, 2},
		{"pod uid changed", func(e *env, hits *atomic.Int32) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write(okResult(t)) }
		}, true, executor.Gone, executor.EndEvicted, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, nil)
			h := e.start("01HBBBBBBBBBBBBBBBBBBBBBBB")
			var hits atomic.Int32
			e.serve(tc.handler(e, &hits))
			if tc.recreate {
				// A pod with the same name but another UID: not ours.
				pod := buildPod(h, testCmd(), e.ex.o)
				e.fake.DeletePod(context.Background(), ns, pod.Meta.Name, 0)
				e.fake.CreatePod(context.Background(), ns, pod)
			}
			e.agentDone(h, 3, 0)
			st := e.waitTerminal(h)
			if st.State != tc.state || st.End != tc.end {
				t.Errorf("status %+v, want %s %q", st, tc.state, tc.end)
			}
			if n := hits.Load(); n < tc.minHits || n > tc.maxHits {
				t.Errorf("server hits %d, want %d..%d", n, tc.minHits, tc.maxHits)
			}
		})
	}
}

func TestEnds(t *testing.T) {
	name := func(h executor.Handle) string { return jobpod.PodName(h.JobID) }
	for _, tc := range []struct {
		name  string
		drive func(e *env, h executor.Handle)
		state executor.State
		end   executor.End
		exit  int
	}{
		{"evicted", func(e *env, h executor.Handle) { e.fake.Evict(ns, name(h)) }, executor.Gone, executor.EndEvicted, 0},
		{"pod deleted", func(e *env, h executor.Handle) { e.fake.DeletePod(context.Background(), ns, name(h), 0) }, executor.Gone, executor.EndEvicted, 0},
		{"image pull back-off", func(e *env, h executor.Handle) {
			e.fake.SetInitContainer(ns, name(h), "pre", api.ContainerState{Waiting: &api.StateWaiting{Reason: "ImagePullBackOff"}})
		}, executor.Gone, executor.EndEvicted, 0},
		{"unschedulable", func(e *env, h executor.Handle) { e.advance(10 * time.Minute) }, executor.Gone, executor.EndEvicted, 0},
		{"pre exit 3", func(e *env, h executor.Handle) { e.preDone(h, 3, "bad\x1b[31m manifest") }, executor.Gone, executor.EndPrepFailed, 0},
		{"pre exit 1", func(e *env, h executor.Handle) { e.preDone(h, 1, "clone timed out") }, executor.Gone, executor.EndCloneFailed, 0},
		{"deadline exceeded", func(e *env, h executor.Handle) {
			e.fake.SetInitContainer(ns, name(h), "pre", api.ContainerState{Terminated: &api.StateTerminated{}})
			e.fake.SetContainer(ns, name(h), "agent", api.ContainerState{Running: &api.StateRunning{}})
			e.fake.SetPhase(ns, name(h), "Failed")
			e.fake.Mutate(ns, name(h), func(p *api.Pod) { p.Status.Reason = "DeadlineExceeded" })
		}, executor.TimedOut, "", 124},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, nil)
			h := e.start("01HCCCCCCCCCCCCCCCCCCCCCCC")
			tc.drive(e, h)
			st := e.waitTerminal(h)
			if st.State != tc.state || st.End != tc.end || st.ExitCode != tc.exit {
				t.Errorf("status %+v", st)
			}
			if strings.ContainsRune(st.Reason, 0x1b) {
				t.Errorf("reason not sanitised: %q", st.Reason)
			}
			if tc.state == executor.Gone && e.tok.nRevoked() != 1 {
				t.Errorf("token revoked %d times", e.tok.nRevoked())
			}
		})
	}
}

func TestStartRollback(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	h, err := e.ex.Prepare(ctx, testJob("01HDDDDDDDDDDDDDDDDDDDDDDD"))
	if err != nil {
		t.Fatal(err)
	}
	e.fake.Err["CreateSecret"] = errors.New("quota")
	if _, err = e.ex.Start(ctx, h, testCmd()); err == nil {
		t.Fatal("want error")
	}
	if _, err := e.fake.GetPod(ctx, ns, jobpod.PodName(h.JobID)); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("pod remains: %v", err)
	}
	if e.tok.nRevoked() != 1 {
		t.Errorf("token revoked %d times", e.tok.nRevoked())
	}
	// A mint failure creates nothing and can be retried.
	delete(e.fake.Err, "CreateSecret")
	e.tok.err = errors.New("github down")
	if _, err = e.ex.Start(ctx, h, testCmd()); err == nil {
		t.Fatal("want mint error")
	}
	e.tok.err = nil
	if _, err = e.ex.Start(ctx, h, testCmd()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err = e.ex.Start(ctx, h, testCmd()); !errors.Is(err, executor.ErrAlreadyStarted) {
		t.Errorf("second start: %v", err)
	}
}

func TestInputsTooLarge(t *testing.T) {
	e := newEnv(t, nil)
	j := testJob("01HEEEEEEEEEEEEEEEEEEEEEEE")
	j.Inputs = []executor.File{{Path: "big.txt", Data: make([]byte, jobpod.MaxManifestBytes)}}
	_, err := e.ex.Prepare(context.Background(), j)
	if !errors.Is(err, executor.ErrInputsTooLarge) {
		t.Fatalf("err = %v", err)
	}
	wt, _ := e.ws.WorktreePath(j.Repo, j.ID)
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree left behind: %v", err)
	}
}

func TestPrepareRefusals(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	j := testJob("01HFFFFFFFFFFFFFFFFFFFFFFF")
	j.RoleSource = executor.RoleSource{}
	if _, err := e.ex.Prepare(ctx, j); !errors.Is(err, executor.ErrInvalidJob) {
		t.Errorf("no role source: %v", err)
	}
	j = testJob(strings.Repeat("A", jobpod.MaxJobID+1))
	if _, err := e.ex.Prepare(ctx, j); !errors.Is(err, executor.ErrInvalidJob) {
		t.Errorf("long id: %v", err)
	}
}

func TestStartRefusals(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	h, err := e.ex.Prepare(ctx, testJob("01HGGGGGGGGGGGGGGGGGGGGGGG"))
	if err != nil {
		t.Fatal(err)
	}
	stdin := testCmd()
	stdin.Stdin = strings.NewReader("x")
	badDir := testCmd()
	badDir.Fixed = []string{"CLAUDE_CONFIG_DIR=/elsewhere"}
	badEnv := testCmd()
	badEnv.Env = []string{"GITHUB_TOKEN=x"}
	for name, c := range map[string]executor.Command{"stdin": stdin, "config dir": badDir, "env": badEnv} {
		if _, err := e.ex.Start(ctx, h, c); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if len(e.fake.Calls()) != 0 || len(e.tok.minted) != 0 {
		t.Errorf("a refused start reached the cluster or minted: %v", e.fake.Calls())
	}
	other := h
	other.JobID = "01HNOTPREPARED"
	if _, err := e.ex.Start(ctx, other, testCmd()); !errors.Is(err, executor.ErrNotPrepared) {
		t.Errorf("not prepared: %v", err)
	}
}

// A fresh executor (the controller restarted) sees a live pod as Orphaned,
// and Cleanup still removes it.
func TestOrphanAfterRestart(t *testing.T) {
	e := newEnv(t, nil)
	h := e.start("01HHHHHHHHHHHHHHHHHHHHHHHH")
	e.ex.get(h.JobID).stop() // the old controller is gone, and so is its watcher
	e.ex.get(h.JobID).wg.Wait()
	e2 := newEnv(t, nil)
	e2.ex.o.Client, e2.ex.o.WS = e.fake, e.ws
	st, err := e2.ex.Status(context.Background(), h)
	if err != nil || st.State != executor.Orphaned {
		t.Fatalf("status %+v %v", st, err)
	}
	if err := e2.ex.Cleanup(context.Background(), h, executor.CleanupOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.fake.GetPod(context.Background(), ns, jobpod.PodName(h.JobID)); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("pod remains: %v", err)
	}
}

func TestCleanupIdempotent(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	h := e.start("01HIIIIIIIIIIIIIIIIIIIIIII")
	for i := 0; i < 2; i++ {
		if err := e.ex.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
			t.Fatalf("cleanup %d: %v", i, err)
		}
	}
	for _, p := range []string{h.Worktree, h.Home, h.Worktree + ".input"} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s remains", p)
		}
	}
	if _, ok := e.fake.Secret(ns, jobpod.PodName(h.JobID)); ok {
		t.Error("secret remains")
	}
	if e.tok.nRevoked() != 1 || e.git.pruned == 0 {
		t.Errorf("revoked %d pruned %d", e.tok.nRevoked(), e.git.pruned)
	}
	if _, err := os.Stat(h.LogPath); err != nil {
		t.Error("cleanup removed the log")
	}

	h2 := e.start("01HJJJJJJJJJJJJJJJJJJJJJJJ")
	if err := e.ex.Cleanup(ctx, h2, executor.CleanupOptions{KeepWorktree: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h2.Worktree); err != nil {
		t.Errorf("KeepWorktree removed the worktree: %v", err)
	}
	// A foreign handle: directories only, the cluster untouched.
	n := len(e.fake.Calls())
	f := h2
	f.Kind = "docker"
	if err := e.ex.Cleanup(ctx, f, executor.CleanupOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(e.fake.Calls()) != n {
		t.Errorf("foreign cleanup called the API: %v", e.fake.Calls()[n:])
	}
	if _, err := os.Stat(f.Worktree); !os.IsNotExist(err) {
		t.Error("foreign worktree remains")
	}
}

func TestSweep(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	keepH := e.start("01HKKKKKKKKKKKKKKKKKKKKKKK")
	dropH := e.start("01HLLLLLLLLLLLLLLLLLLLLLLL")
	// Another repo's pod in the same namespace.
	other := buildPod(dropH, testCmd(), e.ex.o)
	other.Meta.Name = "ard-job-other"
	other.Meta.Labels[jobpod.LabelRepo] = jobpod.RepoLabel("x/y")
	if _, err := e.fake.CreatePod(ctx, ns, other); err != nil {
		t.Fatal(err)
	}
	if err := e.ex.Sweep(ctx, func(id string) bool { return id == keepH.JobID }); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{jobpod.PodName(keepH.JobID): true, jobpod.PodName(dropH.JobID): false, "ard-job-other": true} {
		_, err := e.fake.GetPod(ctx, ns, name)
		if (err == nil) != want {
			t.Errorf("pod %s present = %v, want %v", name, err == nil, want)
		}
	}
	if _, ok := e.fake.Secret(ns, jobpod.PodName(dropH.JobID)); ok {
		t.Error("swept job's secret remains")
	}
}

func TestLogFollowReconnect(t *testing.T) {
	e := newEnv(t, nil)
	e.fake.LogLimit = 2
	h, err := e.ex.Prepare(context.Background(), testJob("01HMMMMMMMMMMMMMMMMMMMMMMM"))
	if err != nil {
		t.Fatal(err)
	}
	e.fake.SetLogs(ns, jobpod.PodName(h.JobID), "agent", nil)
	if h, err = e.ex.Start(context.Background(), h, testCmd()); err != nil {
		t.Fatal(err)
	}
	e.fake.SetLogs(ns, jobpod.PodName(h.JobID), "agent", []string{"a", "b", "c", "d", "e"})
	deadline := time.Now().Add(3 * time.Second)
	for readFile(t, h.LogPath) != "a\nb\nc\nd\ne\n" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := readFile(t, h.LogPath); got != "a\nb\nc\nd\ne\n" {
		t.Errorf("log = %q", got)
	}
	e.ex.Cleanup(context.Background(), h, executor.CleanupOptions{})
}

func TestSwapCrashResume(t *testing.T) {
	dir := t.TempDir()
	h := executor.Handle{Worktree: filepath.Join(dir, "J")}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(h.Worktree, 0o755))
	must(os.WriteFile(filepath.Join(h.Worktree, ".git"), []byte("link"), 0o644))
	must(os.MkdirAll(h.Worktree+".result/work", 0o755))
	must(os.WriteFile(h.Worktree+".result/work/f", []byte("new"), 0o644))
	// Crash after step one: the worktree is already aside.
	must(os.Rename(h.Worktree, h.Worktree+".input"))
	for i := 0; i < 2; i++ { // and a plain repeat after completion
		must(swap(h))
	}
	if readFile(t, filepath.Join(h.Worktree, "f")) != "new" || readFile(t, filepath.Join(h.Worktree, ".git")) != "link" {
		t.Error("swap did not finish")
	}
	must(writeDone(h, doneFile{Exit: 1}))
	must(writeDone(h, doneFile{Exit: 2})) // the first marker stays
	if d, err := readDone(h); err != nil || d.Exit != 1 {
		t.Errorf("done = %+v %v", d, err)
	}
	if fi, _ := os.Stat(donePath(h)); fi.Mode().Perm() != 0o600 {
		t.Errorf("done mode %v", fi.Mode())
	}
}

func TestPruneDelegatesToWorkspace(t *testing.T) {
	e := newEnv(t, nil)
	if err := e.ex.Prune(context.Background(), time.Hour); err != nil {
		t.Fatal(err)
	}
}
