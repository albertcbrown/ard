//go:build unix

package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
)

// track is the in-process record of a running job.
type track struct {
	mu       sync.Mutex
	done     chan struct{}
	timedOut bool
	exited   bool
	code     int
	signal   string
	finished time.Time
	timer    *time.Timer
	pid      int
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func procStart(pid int) string {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", fmt.Sprint(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// verified reports whether pid is alive and is the process we started.
func verified(h executor.Handle) bool {
	return h.PID > 0 && h.ProcStart != "" && alive(h.PID) && procStart(h.PID) == h.ProcStart
}

func (l *Local) track(id string) *track {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tracks[id]
}

// Start launches the command without blocking.
func (l *Local) Start(ctx context.Context, h executor.Handle, c executor.Command) (executor.Handle, error) {
	if h.PID != 0 || l.track(h.JobID) != nil {
		return h, executor.ErrAlreadyStarted
	}
	if st, err := os.Stat(h.Worktree); err != nil || !st.IsDir() {
		return h, executor.ErrNotPrepared
	}
	if err := l.checkEnv(c.Env); err != nil {
		return h, err
	}
	if err := l.checkFixed(c.Fixed); err != nil {
		return h, err
	}
	if err := l.checkConfigDir(h, c.Fixed); err != nil {
		return h, err
	}
	var cmd *exec.Cmd
	if h.Kind == "docker" {
		cmd = exec.Command(l.dockerBin, l.runArgv(h, c, c.Stdin != nil)...)
		for _, kv := range os.Environ() {
			if n, _, _ := strings.Cut(kv, "="); !ghRE.MatchString(n) && n != "SSH_AUTH_SOCK" {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		cmd.Env = append(cmd.Env, c.Env...)
		cmd.Dir = h.Worktree
	} else {
		cmd = exec.Command(c.Path, c.Args...)
		cmd.Env = append(append(l.hostEnv(h), c.Fixed...), c.Env...)
		cmd.Dir = h.Worktree
	}
	cmd.Stdin = c.Stdin // nil = /dev/null
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = l.grace + 5*time.Second

	logf, err := os.OpenFile(h.LogPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return h, fmt.Errorf("open log: %w", err)
	}
	outf, err := os.OpenFile(h.StdoutPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		logf.Close()
		return h, fmt.Errorf("open stdout log: %w", err)
	}
	combined := &lockedWriter{w: logf}
	cmd.Stdout = io.MultiWriter(outf, combined)
	cmd.Stderr = combined

	if err := cmd.Start(); err != nil {
		logf.Close()
		outf.Close()
		return h, fmt.Errorf("start %s: %w", c.Path, err)
	}
	h.PID = cmd.Process.Pid
	h.ProcStart = procStart(h.PID)
	h.StartedAt = l.now().UTC()
	h.Deadline = h.StartedAt.Add(h.Timeout)

	tr := &track{done: make(chan struct{}), pid: h.PID}
	l.mu.Lock()
	l.tracks[h.JobID] = tr
	l.mu.Unlock()
	l.log.Info("executor job started", "job", h.JobID, "issue", h.Issue, "stage", h.Stage,
		"role", h.Role, "kind", h.Kind, "pid", h.PID, "deadline", h.Deadline)
	if hostUntrusted(h) {
		l.warnHostJob(ctx, h)
	}

	hh := h
	tr.timer = time.AfterFunc(h.Timeout, func() {
		tr.mu.Lock()
		if tr.exited {
			tr.mu.Unlock()
			return
		}
		tr.timedOut = true
		tr.mu.Unlock()
		l.stop(hh, tr.done)
	})
	go func() {
		werr := cmd.Wait()
		tr.timer.Stop()
		// Reap stragglers left in the process group.
		syscall.Kill(-hh.PID, syscall.SIGKILL)
		logf.Close()
		outf.Close()
		tr.mu.Lock()
		tr.exited = true
		tr.finished = l.now().UTC()
		tr.code = 0
		if ps := cmd.ProcessState; ps != nil {
			tr.code = ps.ExitCode()
			if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				tr.signal = ws.Signal().String()
			}
		} else if werr != nil {
			tr.code = -1
		}
		timedOut, code, sig := tr.timedOut, tr.code, tr.signal
		tr.mu.Unlock()
		close(tr.done)
		l.log.Info("executor job exited", "job", hh.JobID, "code", code, "signal", sig,
			"timed_out", timedOut, "duration", tr.finished.Sub(hh.StartedAt))
	}()
	return h, nil
}

// stop sends SIGTERM to the process group, then SIGKILL after the grace
// period. It signals only a tracked or verified PID. done may be nil.
func (l *Local) stop(h executor.Handle, done <-chan struct{}) {
	if h.PID <= 0 {
		return // kill(0) would signal the controller's own process group
	}
	if done == nil && !verified(h) {
		return
	}
	syscall.Kill(-h.PID, syscall.SIGTERM)
	if h.Kind == "docker" {
		l.dockerKill("TERM", h)
	}
	wait := func(d time.Duration) bool {
		if done != nil {
			select {
			case <-done:
				return true
			case <-time.After(d):
				return false
			}
		}
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			if !alive(h.PID) {
				return true
			}
			time.Sleep(20 * time.Millisecond)
		}
		return !alive(h.PID)
	}
	if wait(l.grace) {
		return
	}
	syscall.Kill(-h.PID, syscall.SIGKILL)
	if h.Kind == "docker" {
		l.dockerKill("KILL", h)
	}
	wait(5 * time.Second)
}

func (l *Local) lastOutput(h executor.Handle) time.Time {
	if st, err := os.Stat(h.LogPath); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

// Status reports the job state.
func (l *Local) Status(ctx context.Context, h executor.Handle) (executor.Status, error) {
	st := executor.Status{LastOutput: l.lastOutput(h), StartedAt: h.StartedAt}
	if tr := l.track(h.JobID); tr != nil {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		switch {
		case !tr.exited:
			st.State, st.Alive = executor.Running, true
		case tr.timedOut:
			st.State = executor.TimedOut
			st.Reason = "deadline exceeded"
		default:
			st.State = executor.Exited
		}
		if tr.exited {
			st.ExitCode, st.Signal, st.FinishedAt = tr.code, tr.signal, tr.finished
		}
		return st, nil
	}
	if h.PID == 0 {
		if _, err := os.Stat(h.Worktree); err == nil {
			st.State = executor.Prepared
		} else {
			st.State, st.Reason = executor.Gone, "worktree missing"
		}
		return st, nil
	}
	if verified(h) || (h.Kind == "docker" && l.dockerRunning(ctx, h)) {
		st.State, st.Alive = executor.Orphaned, true
		st.Reason = "not tracked by this controller process"
		return st, nil
	}
	st.State, st.Reason = executor.Gone, "process gone; exit code unknown"
	return st, nil
}

// Cleanup stops the job if needed and removes the workspace. Logs stay.
func (l *Local) Cleanup(ctx context.Context, h executor.Handle, o executor.CleanupOptions) error {
	if tr := l.track(h.JobID); tr != nil {
		tr.mu.Lock()
		running := !tr.exited
		tr.mu.Unlock()
		if running {
			h.PID = tr.pid // a Prepare-era handle has PID 0; trust the tracker
			l.stop(h, tr.done)
		}
	} else if verified(h) {
		l.stop(h, nil)
	}
	var errs []error
	if h.Kind == "docker" && h.Container != "" {
		if out, err := l.docker(ctx, "rm", "-f", h.Container); err != nil && !strings.Contains(string(out), "No such container") {
			errs = append(errs, fmt.Errorf("docker rm: %w: %s", err, strings.TrimSpace(string(out))))
		}
	}
	if !o.KeepWorktree {
		foreign := l.cloneDir != "" && filepath.Clean(h.RepoDir) != l.cloneDir
		if _, err := os.Stat(h.Worktree); err == nil {
			chmodWritable(h.Worktree)
			if foreign {
				// Stored before #113: RepoDir is the operator's checkout, where
				// ard never runs git. The registration there dangles until the
				// operator's own `git worktree prune`.
				l.log.Info("executor.cleanup_foreign_repo", "job", h.JobID, "repo_dir", h.RepoDir, "clone", l.cloneDir)
				if err := os.RemoveAll(h.Worktree); err != nil {
					errs = append(errs, err)
				}
			} else if err := l.git.WorktreeRemove(context.WithoutCancel(ctx), h.RepoDir, h.Worktree, true); err != nil {
				errs = append(errs, err)
				os.RemoveAll(h.Worktree)
			}
		}
		chmodWritable(filepath.Join(h.Home, sidecarDirName)) // a-w tree; derived from Home, not h.SidecarDir
		if err := os.RemoveAll(h.Home); err != nil {
			errs = append(errs, err)
		}
		if !foreign {
			if err := l.git.WorktreePrune(context.WithoutCancel(ctx), h.RepoDir); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if tr := l.track(h.JobID); tr != nil {
		select {
		case <-tr.done:
			l.mu.Lock()
			delete(l.tracks, h.JobID)
			l.mu.Unlock()
		default:
		}
	}
	return errors.Join(errs...)
}
