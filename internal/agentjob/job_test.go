package agentjob

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/state"
)

type comments struct{ cs []github.Comment }

func (c *comments) ListComments(context.Context, int, time.Time) ([]github.Comment, error) {
	return c.cs, nil
}

func (c *comments) CreateComment(_ context.Context, _ int, body string) (github.Comment, error) {
	c.cs = append(c.cs, github.Comment{Body: body})
	return github.Comment{Body: body}, nil
}

func TestPostOnce(t *testing.T) {
	c := &comments{}
	for i := 0; i < 2; i++ {
		if err := PostOnce(context.Background(), c, c, 5, "<!-- m -->", "text"); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.cs) != 1 || c.cs[0].Body != "<!-- m -->\ntext" {
		t.Errorf("comments = %+v", c.cs)
	}
	if err := PostOnce(context.Background(), c, c, 5, "<!-- other -->", "x"); err != nil || len(c.cs) != 2 {
		t.Errorf("a different marker must post: %v %d", err, len(c.cs))
	}
}

type admissions struct {
	a   state.Admission
	err error
}

func (s *admissions) Admission(context.Context, int) (state.Admission, error) { return s.a, s.err }
func (s *admissions) SetAdmission(_ context.Context, a state.Admission) error { s.a = a; return nil }

func TestMarkRegated(t *testing.T) {
	t1, t2 := t0, t0.Add(time.Hour)
	s := &admissions{a: state.Admission{Issue: 3}}
	if err := MarkRegated(context.Background(), s, github.Issue{Number: 3, UpdatedAt: t1}, "h1"); err != nil {
		t.Fatal(err)
	}
	if !s.a.RegatedAt.Equal(t1) || s.a.RegatedHash != "h1" {
		t.Fatalf("admission = %+v", s.a)
	}
	// The same hash again keeps the first updated_at.
	if err := MarkRegated(context.Background(), s, github.Issue{Number: 3, UpdatedAt: t2}, "h1"); err != nil || !s.a.RegatedAt.Equal(t1) {
		t.Errorf("same hash moved the mark: %v %+v", err, s.a)
	}
	if err := MarkRegated(context.Background(), s, github.Issue{Number: 3, UpdatedAt: t2}, "h2"); err != nil || !s.a.RegatedAt.Equal(t2) || s.a.RegatedHash != "h2" {
		t.Errorf("new hash: %v %+v", err, s.a)
	}
	bad := &admissions{err: state.ErrNotFound}
	if err := MarkRegated(context.Background(), bad, github.Issue{Number: 3}, "h"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

type fakeExec struct{ calls int }

func (f *fakeExec) Prepare(context.Context, executor.Job) (executor.Handle, error) {
	return executor.Handle{}, nil
}
func (f *fakeExec) Start(context.Context, executor.Handle, executor.Command) (executor.Handle, error) {
	return executor.Handle{}, nil
}
func (f *fakeExec) Status(context.Context, executor.Handle) (executor.Status, error) {
	f.calls++
	return executor.Status{State: executor.Running}, nil
}
func (f *fakeExec) Logs(context.Context, executor.Handle, executor.LogsOptions) (io.ReadCloser, error) {
	return nil, errors.New("no logs")
}
func (f *fakeExec) Cleanup(context.Context, executor.Handle, executor.CleanupOptions) error {
	return nil
}

func TestWaitStops(t *testing.T) {
	stopping := make(chan struct{})
	ex := &fakeExec{}
	done := make(chan error, 1)
	go func() {
		_, err := Wait(context.Background(), ex, executor.Handle{}, time.Millisecond, stopping)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	close(stopping)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return on shutdown")
	}
	if ex.calls == 0 {
		t.Error("status never polled")
	}
}

type handles struct {
	got string
	err error
}

func (h *handles) SetHandle(_ context.Context, id string, b json.RawMessage) error {
	h.got = id + ":" + string(b)
	return h.err
}

func TestSaveHandle(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &handles{}
	SaveHandle(context.Background(), h, log, "J1", executor.Handle{JobID: "J1"})
	if !strings.HasPrefix(h.got, "J1:{") {
		t.Errorf("got %q", h.got)
	}
	SaveHandle(context.Background(), &handles{err: errors.New("x")}, log, "J1", executor.Handle{}) // logged, not fatal
}
