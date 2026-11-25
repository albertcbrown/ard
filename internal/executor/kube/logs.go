package kube

import (
	"bufio"
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/kube/api"
	"github.com/albertcbrown/ard/internal/jobpod"
)

// followLogs copies the agent container's log into h.LogPath, without the
// timestamps. It reconnects from the last timestamp when the stream ends, and
// stops after one more full read once the agent has ended.
func (e *Executor) followLogs(ctx context.Context, h executor.Handle, lj *liveJob) {
	f, err := os.OpenFile(h.LogPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		e.o.Log.Warn("kube.log_open_failed", "job", h.JobID, "err", err)
		return
	}
	defer f.Close()
	var last time.Time
	name := jobpod.PodName(h.JobID)
	for {
		ended := lj.agentEnded.Load()
		rc, err := e.o.Client.PodLogs(ctx, e.o.Namespace, name, jobpod.ContainerAgent, last)
		switch {
		case ended && err != nil, errors.Is(err, api.ErrNotFound) && ended, ctx.Err() != nil:
			return
		case err == nil:
			sc := bufio.NewScanner(rc)
			sc.Buffer(make([]byte, 64<<10), 1<<20)
			for sc.Scan() {
				ts, rest, ok := strings.Cut(sc.Text(), " ")
				t, perr := time.Parse(time.RFC3339Nano, ts)
				if !ok || perr != nil || !t.After(last) {
					continue
				}
				last = t
				f.WriteString(rest + "\n")
			}
			rc.Close()
			if ended {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(e.o.Poll):
		}
	}
}
