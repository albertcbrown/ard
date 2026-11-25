package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/kube/api"
	"github.com/albertcbrown/ard/internal/jobpod"
	"github.com/albertcbrown/ard/internal/tarball"
)

const maxReason = 200

// sanitize makes a pod-supplied message safe to log and store: printable
// ASCII only and capped.
func sanitize(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s) && len(b) < maxReason; i++ {
		if c := s[i]; c >= 0x20 && c < 0x7f {
			b = append(b, c)
		} else {
			b = append(b, '?')
		}
	}
	return strings.TrimSpace(string(b))
}

func containerState(list []api.ContainerStatus, name string) (api.ContainerState, bool) {
	for _, c := range list {
		if c.Name == name {
			return c.State, true
		}
	}
	return api.ContainerState{}, false
}

func signalName(n int32) string {
	switch n {
	case 0:
		return ""
	case 9:
		return "killed"
	case 15:
		return "terminated"
	}
	return fmt.Sprintf("signal %d", n)
}

// watch polls the pod until the job has ended, then leaves the .done marker.
// It runs one goroutine per job. Once the marker is written it returns.
func (e *Executor) watch(ctx context.Context, h executor.Handle, lj *liveJob) {
	t := time.NewTicker(e.o.Poll)
	defer t.Stop()
	for {
		if e.step(ctx, h, lj) {
			e.mu.Lock()
			lj.agentEnded.Store(true)
			e.mu.Unlock()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// end writes a marker for a job that ended without a result.
func (e *Executor) end(h executor.Handle, why executor.End, reason string) bool {
	e.o.Log.Warn("kube.job_ended", "job", h.JobID, "end", string(why), "reason", reason)
	return e.finish(h, doneFile{End: why, Reason: sanitize(reason), Finished: e.o.Now().UTC()})
}

func (e *Executor) finish(h executor.Handle, d doneFile) bool {
	if err := writeDone(h, d); err != nil {
		e.o.Log.Error("kube.done_write_failed", "job", h.JobID, "err", err)
		return false // try again on the next tick
	}
	return true
}

// step runs one poll and reports whether the job is finished.
func (e *Executor) step(ctx context.Context, h executor.Handle, lj *liveJob) bool {
	if !exists(h.Worktree) && !exists(h.Worktree+".input") {
		return true // Cleanup removed the job's directories: nothing to record
	}
	now := e.o.Now()
	pod, err := e.o.Client.GetPod(ctx, e.o.Namespace, jobpod.PodName(h.JobID))
	agentEnded := lj.agentEnded.Load()
	switch {
	case errors.Is(err, api.ErrNotFound):
		e.revoke(ctx, lj)
		return e.end(h, executor.EndEvicted, "pod is gone before the agent ended")
	case err != nil:
		if now.After(h.Deadline.Add(e.o.ResultGrace)) {
			return e.end(h, executor.EndNoResult, "pod status unavailable past the deadline")
		}
		return false
	case pod.Meta.UID != lj.uid:
		e.revoke(ctx, lj)
		return e.end(h, executor.EndEvicted, "pod was replaced")
	}
	st := pod.Status
	if s, ok := containerState(st.InitContainerStatuses, jobpod.ContainerPre); ok && s.Terminated != nil {
		e.revoke(ctx, lj)
		if code := s.Terminated.ExitCode; code != 0 {
			why := executor.EndCloneFailed
			if code == jobpod.ExitPermanent {
				why = executor.EndPrepFailed
			}
			return e.end(h, why, s.Terminated.Message)
		}
	}
	a, haveAgent := containerState(st.ContainerStatuses, jobpod.ContainerAgent)
	if haveAgent && a.Terminated != nil {
		if !agentEnded {
			lj.agentEnded.Store(true)
		}
		return e.pull(ctx, h, lj, pod, a.Terminated)
	}
	// The agent has not ended.
	if st.Reason == "DeadlineExceeded" {
		return e.finish(h, doneFile{Exit: exitTimeout, TimedOut: true, Finished: now.UTC(), Reason: "pod deadline exceeded"})
	}
	if st.Reason == "Evicted" || st.Phase == "Failed" || st.Phase == "Succeeded" {
		e.revoke(ctx, lj)
		return e.end(h, executor.EndEvicted, firstNonEmpty(st.Message, st.Reason, "pod "+st.Phase))
	}
	for _, list := range [][]api.ContainerStatus{st.InitContainerStatuses, st.ContainerStatuses} {
		for _, c := range list {
			if w := c.State.Waiting; w != nil && (w.Reason == "ErrImagePull" || w.Reason == "ImagePullBackOff") {
				e.revoke(ctx, lj)
				return e.end(h, executor.EndEvicted, c.Name+": "+w.Reason+": "+w.Message)
			}
		}
	}
	if st.Phase == "Pending" && now.Sub(h.StartedAt) > e.o.PendingTimeout {
		if s, ok := containerState(st.InitContainerStatuses, jobpod.ContainerPre); !ok || s.Waiting != nil {
			e.revoke(ctx, lj)
			return e.end(h, executor.EndEvicted, "pod not scheduled in time")
		}
	}
	return false
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// pull fetches the result once. It reports true when the job is finished; a
// retryable failure returns false and is tried again on the next poll until
// Deadline+ResultGrace.
func (e *Executor) pull(ctx context.Context, h executor.Handle, lj *liveJob, pod *api.Pod, term *api.StateTerminated) bool {
	cut := h.Deadline.Add(e.o.ResultGrace)
	give := func(reason string) bool {
		if e.o.Now().After(cut) {
			return e.end(h, executor.EndNoResult, reason)
		}
		return false
	}
	ip := pod.Status.PodIP
	if ip == "" {
		return give("pod has no IP")
	}
	res := h.Worktree + ".result"
	os.RemoveAll(res)
	if err := os.Mkdir(res, 0o700); err != nil {
		return give(err.Error())
	}
	u := fmt.Sprintf("http://%s:%d%s", ip, jobpod.ResultPort, jobpod.ResultPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return give(err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+lj.key)
	resp, err := e.o.HTTP.Do(req)
	if err != nil {
		return give("result pull: " + sanitize(err.Error()))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return give(fmt.Sprintf("result pull: status %d", resp.StatusCode))
	}
	if _, err := tarball.Unpack(resp.Body, res, e.o.Limits); err != nil {
		var te *tarball.Error
		if errors.As(err, &te) {
			os.RemoveAll(res)
			return e.end(h, executor.EndBadResult, te.Error())
		}
		return give("result pull: " + sanitize(err.Error()))
	}
	if fi, err := os.Lstat(filepath.Join(res, jobpod.RootWork)); err != nil || !fi.IsDir() {
		os.RemoveAll(res)
		return e.end(h, executor.EndBadResult, "result has no work tree")
	}
	if err := copyStdout(filepath.Join(res, jobpod.RootOut, jobpod.StdoutName), h.StdoutPath); err != nil {
		e.o.Log.Warn("kube.stdout_copy_failed", "job", h.JobID, "err", err)
	}
	if err := swap(h); err != nil {
		e.o.Log.Error("kube.swap_failed", "job", h.JobID, "err", err)
		return false
	}
	d := doneFile{Exit: int(term.ExitCode), Signal: signalName(term.Signal), Finished: term.FinishedAt}
	if term.Signal != 0 {
		d.Exit = -1
	}
	if d.Finished.IsZero() {
		d.Finished = e.o.Now().UTC()
	}
	return e.finish(h, d)
}

// copyStdout copies the packed stdout file, if any. It opens src without
// following links; the unpacked tree holds no symlink that escapes it.
func copyStdout(src, dst string) error {
	if dst == "" {
		return nil
	}
	fi, err := os.Lstat(src)
	if err != nil || !fi.Mode().IsRegular() {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	return errors.Join(cerr, out.Close())
}
