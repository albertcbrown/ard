package budget

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/state"
)

var update = flag.Bool("update", false, "rewrite golden files")

type fakeCapper struct {
	caps []Cap
	now  *time.Time
}

func (f *fakeCapper) Capped(context.Context) ([]Cap, time.Time, error) {
	return f.caps, utcDayStart(*f.now), nil
}

type fakeFx struct {
	bodies []string
	nums   []int
	err    error
	dry    bool
}

func (f *fakeFx) CreateComment(_ context.Context, n int, body string) (github.Comment, error) {
	if f.err != nil {
		return github.Comment{}, f.err
	}
	f.bodies = append(f.bodies, body)
	f.nums = append(f.nums, n)
	return github.Comment{}, nil
}
func (f *fakeFx) DryRun() bool { return f.dry }

type noticeEnv struct {
	dir    string
	now    time.Time
	capper *fakeCapper
	fx     *fakeFx
	issue  int
	st     *state.Store
}

func newNoticeEnv(t *testing.T, caps ...Cap) *noticeEnv {
	t.Helper()
	e := &noticeEnv{dir: t.TempDir(), now: time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC), fx: &fakeFx{}, issue: 7}
	e.capper = &fakeCapper{caps: caps, now: &e.now}
	st, err := state.Open(context.Background(), e.dir, state.Options{Now: func() time.Time { return e.now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e.st = st
	return e
}

func (e *noticeEnv) loop() *Loop {
	return NewLoop(Deps{Gate: e.capper, Store: e.st, Effects: e.fx, StatusIssue: func() int { return e.issue }, Now: func() time.Time { return e.now }})
}

func (e *noticeEnv) tick(t *testing.T, l *Loop) {
	t.Helper()
	if err := l.Tick(context.Background(), controller.Env{}); err != nil {
		t.Fatal(err)
	}
}

var (
	repoCap = Cap{Scope: "repo", Spent: state.Spend{USD: 50.5}, Limit: 50}
	engCap  = Cap{Scope: "role", Stage: "worker", Role: "eng", Spent: state.Spend{USD: 10}, Limit: 10}
	revCap  = Cap{Scope: "role", Stage: "review", Role: "rev", Spent: state.Spend{USD: 6, Unpriced: 3}, Limit: 5}
)

func TestNoticeOncePerScopePerDay(t *testing.T) {
	e := newNoticeEnv(t, repoCap)
	l := e.loop()
	e.tick(t, l)
	e.tick(t, l)
	if len(e.fx.bodies) != 1 || e.fx.nums[0] != 7 {
		t.Fatalf("comments %v", e.fx.nums)
	}
	evs, _ := e.st.Events(context.Background(), state.EventFilter{Kind: state.EventBudgetCapped})
	if len(evs) != 1 {
		t.Fatalf("events %d", len(evs))
	}
}

func TestNoticeAcrossRestart(t *testing.T) {
	e := newNoticeEnv(t, repoCap)
	e.tick(t, e.loop())
	e.tick(t, e.loop())
	if len(e.fx.bodies) != 1 {
		t.Fatalf("comments %d", len(e.fx.bodies))
	}
}

func TestNoticeNextUTCDay(t *testing.T) {
	e := newNoticeEnv(t, repoCap)
	e.now = time.Date(2026, 3, 4, 23, 59, 0, 0, time.UTC)
	l := e.loop()
	e.tick(t, l)
	e.now = e.now.Add(2 * time.Minute)
	e.tick(t, l)
	if len(e.fx.bodies) != 2 || !strings.Contains(e.fx.bodies[1], "2026-03-05") {
		t.Fatalf("bodies %q", e.fx.bodies)
	}
}

func TestNoticeScopesIndependent(t *testing.T) {
	e := newNoticeEnv(t, repoCap, engCap, revCap)
	l := e.loop()
	e.tick(t, l)
	e.tick(t, l)
	if len(e.fx.bodies) != 3 {
		t.Fatalf("comments %d", len(e.fx.bodies))
	}
}

func TestNoticeNoStatusIssue(t *testing.T) {
	e := newNoticeEnv(t, repoCap)
	e.issue = 0
	l := e.loop()
	e.tick(t, l)
	evs, _ := e.st.Events(context.Background(), state.EventFilter{Kind: state.EventBudgetCapped})
	if len(e.fx.bodies) != 0 || len(evs) != 0 {
		t.Fatal("posted before the status issue existed")
	}
	e.issue = 9
	e.tick(t, l)
	if len(e.fx.bodies) != 1 || e.fx.nums[0] != 9 {
		t.Fatalf("comments %v", e.fx.nums)
	}
}

func TestNoticeCommentFails(t *testing.T) {
	e := newNoticeEnv(t, repoCap)
	l := e.loop()
	e.fx.err = errors.New("boom")
	e.tick(t, l)
	evs, _ := e.st.Events(context.Background(), state.EventFilter{Kind: state.EventBudgetCapped})
	if len(evs) != 0 {
		t.Fatal("event written after failed comment")
	}
	e.fx.err = nil
	e.tick(t, l)
	if len(e.fx.bodies) != 1 {
		t.Fatalf("comments %d", len(e.fx.bodies))
	}
}

func TestNoticeDryRun(t *testing.T) {
	e := newNoticeEnv(t, repoCap)
	e.fx.dry = true
	e.issue = -3
	l := e.loop()
	e.tick(t, l)
	e.tick(t, l)
	if len(e.fx.bodies) != 1 || e.fx.nums[0] != -3 {
		t.Fatalf("comments %v", e.fx.nums)
	}
}

func TestNoticeEventFields(t *testing.T) {
	e := newNoticeEnv(t, revCap)
	e.tick(t, e.loop())
	evs, _ := e.st.Events(context.Background(), state.EventFilter{Kind: state.EventBudgetCapped})
	if len(evs) != 1 || evs[0].Stage != "review" || evs[0].Role != "rev" {
		t.Fatalf("events %+v", evs)
	}
	d := string(evs[0].Data)
	for _, w := range []string{`"scope":"role"`, `"day":"2026-03-04"`, `"spent_usd":6`, `"cap_usd":5`, `"unpriced":3`} {
		if !strings.Contains(d, w) {
			t.Errorf("data %s lacks %s", d, w)
		}
	}
}

func TestNoticeYesterdayDoesNotSuppress(t *testing.T) {
	e := newNoticeEnv(t, repoCap)
	e.now = time.Date(2026, 3, 4, 23, 59, 30, 0, time.UTC)
	e.tick(t, e.loop())
	e.now = time.Date(2026, 3, 5, 0, 0, 30, 0, time.UTC)
	e.tick(t, e.loop())
	if len(e.fx.bodies) != 2 {
		t.Fatalf("comments %d", len(e.fx.bodies))
	}
}

func TestRenderGolden(t *testing.T) {
	for name, c := range map[string]Cap{"repo": repoCap, "role": engCap, "unpriced": revCap} {
		got := Render(c, "2026-03-04")
		path := filepath.Join("testdata", name+".golden")
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("%s differs:\n%s", name, got)
		}
	}
}

func TestRenderNoAtNoAgentText(t *testing.T) {
	got := Render(Cap{Scope: "role", Stage: "worker", Role: "@evil", Spent: state.Spend{USD: 1}, Limit: 1}, "2026-03-04")
	if strings.Contains(got, "@") || !strings.Contains(got, "estimate") || !strings.Contains(got, "budget_usd_per_day") {
		t.Fatalf("bad comment:\n%s", got)
	}
}
