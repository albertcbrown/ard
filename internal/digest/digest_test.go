package digest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/state"
)

type fakeStore struct {
	events   []state.Event
	from, to time.Time
	jobs     map[state.RoleKey]state.JobCounts
	spend    state.Spend
}

func (f *fakeStore) AppendEvent(_ context.Context, e state.Event) error {
	f.events = append(f.events, e)
	return nil
}

func (f *fakeStore) Events(_ context.Context, fl state.EventFilter) ([]state.Event, error) {
	var out []state.Event
	for _, e := range f.events {
		if e.Kind == fl.Kind && !e.At.Before(fl.Since) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeStore) CountEndedSince(_ context.Context, from, to time.Time) (map[state.RoleKey]state.JobCounts, error) {
	f.from, f.to = from, to
	return f.jobs, nil
}

func (f *fakeStore) SpendBetween(context.Context, time.Time, time.Time) (map[state.RoleKey]state.Spend, state.Spend, error) {
	return nil, f.spend, nil
}

type fakeReader struct {
	issues []github.Issue
	err    error
}

func (r fakeReader) ListIssues(context.Context, github.ListIssuesOpts) ([]github.Issue, error) {
	return r.issues, r.err
}

type fakeEffects struct {
	bodies []string
	err    error
	dry    bool
}

func (e *fakeEffects) CreateComment(_ context.Context, _ int, body string) (github.Comment, error) {
	if e.err != nil {
		return github.Comment{}, e.err
	}
	e.bodies = append(e.bodies, body)
	return github.Comment{}, nil
}
func (e *fakeEffects) DryRun() bool { return e.dry }

func setup(now *time.Time) (*Loop, *fakeStore, *fakeEffects, *fakeReader) {
	st := &fakeStore{}
	fx := &fakeEffects{}
	rd := &fakeReader{}
	l := NewLoop(Deps{Store: st, Reader: rd, Effects: fx, StatusIssue: func() int { return 1 }, Now: func() time.Time { return *now }})
	return l, st, fx, rd
}

var noon = time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)

func TestTickPostsOnce(t *testing.T) {
	now := noon
	l, st, fx, _ := setup(&now)
	for i := 0; i < 2; i++ {
		if err := l.Tick(context.Background(), controllerEnv()); err != nil {
			t.Fatal(err)
		}
	}
	if len(fx.bodies) != 1 || len(st.events) != 1 {
		t.Fatalf("posts = %d, events = %d; want 1, 1", len(fx.bodies), len(st.events))
	}
	if !strings.Contains(fx.bodies[0], "day=2026-03-04") {
		t.Errorf("body = %q", fx.bodies[0])
	}
	// Restart: a new loop over the same store posts nothing; the next day posts.
	l2 := NewLoop(l.d)
	_ = l2.Tick(context.Background(), controllerEnv())
	if len(fx.bodies) != 1 {
		t.Errorf("restart reposted")
	}
	now = noon.Add(24 * time.Hour)
	_ = l2.Tick(context.Background(), controllerEnv())
	if len(fx.bodies) != 2 {
		t.Errorf("next day posts = %d, want 2", len(fx.bodies))
	}
}

func TestTickNoStatusIssue(t *testing.T) {
	now := noon
	l, st, fx, _ := setup(&now)
	l.d.StatusIssue = func() int { return 0 }
	_ = l.Tick(context.Background(), controllerEnv())
	if len(fx.bodies)+len(st.events) != 0 {
		t.Error("posted without a status issue")
	}
}

func TestTickPostFailsNoEvent(t *testing.T) {
	now := noon
	l, st, fx, _ := setup(&now)
	fx.err = errors.New("boom")
	_ = l.Tick(context.Background(), controllerEnv())
	if len(st.events) != 0 {
		t.Fatal("event stored after a failed post")
	}
	fx.err = nil
	_ = l.Tick(context.Background(), controllerEnv())
	if len(fx.bodies) != 1 {
		t.Error("no retry")
	}
}

func TestTickDryRun(t *testing.T) {
	now := noon
	l, st, fx, _ := setup(&now)
	fx.dry = true
	_ = l.Tick(context.Background(), controllerEnv())
	if len(fx.bodies)+len(st.events) != 0 {
		t.Error("dry run posted or stored")
	}
}

func TestTickReaderError(t *testing.T) {
	now := noon
	l, _, fx, rd := setup(&now)
	rd.err = errors.New("api")
	if err := l.Tick(context.Background(), controllerEnv()); err == nil || len(fx.bodies) != 0 {
		t.Errorf("err = %v, posts = %d", err, len(fx.bodies))
	}
}

func TestWindowPreviousDay(t *testing.T) {
	now := noon
	l, st, _, _ := setup(&now)
	_ = l.Tick(context.Background(), controllerEnv())
	if !st.from.Equal(time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)) || !st.to.Equal(time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("window = [%v, %v)", st.from, st.to)
	}
}

func TestQueueDepthByLabel(t *testing.T) {
	now := noon
	l, _, fx, rd := setup(&now)
	rd.issues = []github.Issue{
		{Number: 2, Labels: []string{"ard:pending", "bug"}},
		{Number: 3, Labels: []string{"ard:pending", "ard:hold"}},
		{Number: 4, IsPR: true, Labels: []string{"ard:hold"}},
	}
	_ = l.Tick(context.Background(), controllerEnv())
	b := fx.bodies[0]
	if !strings.Contains(b, "| `ard:pending` | 2 |") || !strings.Contains(b, "| `ard:hold` | 1 |") || strings.Contains(b, "bug") {
		t.Errorf("body = %s", b)
	}
}

func TestRenderZeroAndUnsafe(t *testing.T) {
	zero := Render(Data{Day: "2026-03-04"})
	for _, want := range []string{"<!-- ard:digest day=2026-03-04 -->", "**Jobs run:** 0", "**Failures:** 0", "**Spend:** $0.00"} {
		if !strings.Contains(zero, want) {
			t.Errorf("zero digest lacks %q:\n%s", want, zero)
		}
	}
	d := Data{
		Day:   "2026-03-04",
		Queue: map[string]int{"ard:x @evil": 1},
		Jobs:  map[state.RoleKey]state.JobCounts{{Stage: "worker", Role: "Bad Role@x"}: {Done: 1, Failed: 2}},
		Total: state.Spend{USD: 1.5, Unpriced: 2},
	}
	out := Render(d)
	if strings.Contains(out, "@") || !strings.Contains(out, "worker/? | 2") || !strings.Contains(out, "2 steps reported no cost") || !strings.Contains(out, "**Jobs run:** 3") {
		t.Errorf("render:\n%s", out)
	}
}

func TestIntervalAndClaims(t *testing.T) {
	l := NewLoop(Deps{})
	if l.Claims() || l.Name() != "digest" || l.Interval(nil) != 10*time.Minute {
		t.Error("loop identity")
	}
}

func controllerEnv() controller.Env { return controller.Env{} }
