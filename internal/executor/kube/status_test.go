//go:build unix

package kube

import (
	"errors"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/kube/api"
)

func TestMapStatus(t *testing.T) {
	started := executor.Handle{Kind: Kind, StartedAt: time.Unix(100, 0)}
	pod := &api.Pod{}
	for _, tc := range []struct {
		name  string
		h     executor.Handle
		o     obs
		state executor.State
		end   executor.End
		exit  int
	}{
		{"prepared", executor.Handle{Kind: Kind}, obs{wtExists: true}, executor.Prepared, "", 0},
		{"prepared worktree missing", executor.Handle{Kind: Kind}, obs{}, executor.Gone, "", 0},
		{"foreign kind", executor.Handle{Kind: "docker", StartedAt: time.Unix(1, 0)}, obs{pod: pod, held: true}, executor.Gone, "", 0},
		{"done exit", started, obs{done: &doneFile{Exit: 3}}, executor.Exited, "", 3},
		{"done 124", started, obs{done: &doneFile{Exit: 124}}, executor.TimedOut, "", 124},
		{"done deadline exceeded", started, obs{done: &doneFile{TimedOut: true, Exit: 124}}, executor.TimedOut, "", 124},
		{"done with end", started, obs{done: &doneFile{End: executor.EndEvicted, Reason: "x"}}, executor.Gone, executor.EndEvicted, 0},
		{"orphaned", started, obs{pod: pod}, executor.Orphaned, "", 0},
		{"running", started, obs{pod: pod, held: true}, executor.Running, "", 0},
		{"api error is not gone", started, obs{podErr: errors.New("boom"), held: true}, executor.Running, "", 0},
		{"pod gone", started, obs{podErr: api.ErrNotFound, held: true}, executor.Gone, executor.EndNoResult, 0},
		{"pod gone not held", started, obs{podErr: api.ErrNotFound}, executor.Gone, executor.EndNoResult, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := mapStatus(tc.h, tc.o)
			if st.State != tc.state || st.End != tc.end || st.ExitCode != tc.exit {
				t.Errorf("got %+v, want state %s end %q exit %d", st, tc.state, tc.end, tc.exit)
			}
			if st.Alive != (tc.state == executor.Running || tc.state == executor.Orphaned) {
				t.Errorf("alive = %v", st.Alive)
			}
		})
	}
}
