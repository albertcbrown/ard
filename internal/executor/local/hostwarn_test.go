//go:build unix

package local

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/albertcbrown/ard/internal/executor"
)

func TestHostUntrusted(t *testing.T) {
	tests := []struct {
		name string
		h    executor.Handle
		want bool
	}{
		{"host untrusted", executor.Handle{Kind: "host"}, true},
		{"host trusted", executor.Handle{Kind: "host", TrustedInput: true}, false},
		{"docker untrusted", executor.Handle{Kind: "docker"}, false},
	}
	for _, tc := range tests {
		if got := hostUntrusted(tc.h); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// warnFixture is a fixture whose executor logs to a buffer and records every
// OnHostUntrusted call.
type warnFixture struct {
	*fixture
	mu    sync.Mutex
	logs  bytes.Buffer
	calls []string
	err   error
}

func newWarnFixture(t *testing.T) *warnFixture {
	t.Helper()
	w := &warnFixture{fixture: newFixture(t)}
	opts := w.opts
	opts.Log = slog.New(slog.NewTextHandler(&syncLog{mu: &w.mu, b: &w.logs}, nil))
	opts.OnHostUntrusted = func(_ context.Context, h executor.Handle) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.calls = append(w.calls, h.JobID)
		return w.err
	}
	l, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	w.l = l
	return w
}

type syncLog struct {
	mu *sync.Mutex
	b  *bytes.Buffer
}

func (w *syncLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *warnFixture) logText() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.logs.String()
}

func TestHostJobWarns(t *testing.T) {
	w := newWarnFixture(t)
	j := w.job(20e9)
	j.Stage = "triage"
	_, st := w.run(t, j, agent(t, "ok"))
	if st.State != executor.Exited || st.ExitCode != 0 {
		t.Fatalf("status %+v", st)
	}
	logs := w.logText()
	if n := strings.Count(logs, "executor.host_untrusted"); n != 1 || !strings.Contains(logs, "stage=triage") || !strings.Contains(logs, "job="+j.ID) {
		t.Errorf("want one warning for the triage job:\n%s", logs)
	}
	if len(w.calls) != 1 || w.calls[0] != j.ID {
		t.Errorf("hook calls = %v, want [%s]", w.calls, j.ID)
	}
}

func TestTrustedScannerJobDoesNotWarn(t *testing.T) {
	w := newWarnFixture(t)
	j := w.job(20e9)
	j.Stage, j.TrustedInput = "scanner", true
	w.run(t, j, agent(t, "ok"))
	if logs := w.logText(); strings.Contains(logs, "executor.host_untrusted") {
		t.Errorf("trusted scanner warned:\n%s", logs)
	}
	if len(w.calls) != 0 {
		t.Errorf("hook calls = %v, want none", w.calls)
	}
}

func TestHostHookErrorDoesNotFailJob(t *testing.T) {
	w := newWarnFixture(t)
	w.err = errors.New("db down")
	_, st := w.run(t, w.job(20e9), agent(t, "ok"))
	if st.State != executor.Exited || st.ExitCode != 0 {
		t.Fatalf("status %+v", st)
	}
	if logs := w.logText(); !strings.Contains(logs, "executor.host_untrusted_not_recorded") {
		t.Errorf("missing not_recorded line:\n%s", logs)
	}
}

func TestPrepareCopiesTrustedInput(t *testing.T) {
	f := newFixture(t)
	j := f.job(20e9)
	j.Stage, j.TrustedInput = "scanner", true
	h, err := f.l.Prepare(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.l.Cleanup(context.Background(), h, executor.CleanupOptions{}) })
	if !h.TrustedInput {
		t.Error("Prepare dropped TrustedInput")
	}
}
