package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/state/sqlitex"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

var t0 = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, _ := newStoreClock(t, t.TempDir())
	return s
}

func newStoreClock(t *testing.T, dir string) (*Store, *clock) {
	t.Helper()
	c := &clock{t: t0}
	s, err := Open(tctx(t), dir, Options{Now: c.now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, c
}

func TestDir(t *testing.T) {
	for _, tc := range []struct {
		repo   string
		dry    bool
		want   string
		wantOK bool
	}{
		{"o/r", false, "/root/state/o/r", true},
		{"o/r", true, "/root/state-dryrun/o/r", true},
		{"a-b/c", false, "/root/state/a-b/c", true},
		{"a/b-c", false, "/root/state/a/b-c", true},
		{"Foo/Bar", false, "/root/state/foo/bar", true},
		{"foo/bar", false, "/root/state/foo/bar", true},
		{"./b", false, "", false}, {`a\b/c`, false, "", false},
		{"", false, "", false}, {"a", false, "", false}, {"a/b/c", false, "", false},
		{"/b", false, "", false}, {"a/", false, "", false}, {"../x", false, "", false},
	} {
		got, err := Dir("/root", tc.repo, tc.dry)
		if (err == nil) != tc.wantOK || got != tc.want {
			t.Errorf("Dir(%q,%v) = %q, %v", tc.repo, tc.dry, got, err)
		}
	}
}

func TestEnsureDirMode(t *testing.T) {
	d := filepath.Join(t.TempDir(), "a", "b")
	if err := EnsureDir(d); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(d)
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("mode %o", fi.Mode().Perm())
	}
}

func TestOpenSchemaTooNew(t *testing.T) {
	dir := t.TempDir()
	s, _ := newStoreClock(t, dir)
	if _, err := s.db.ExecContext(tctx(t), `INSERT INTO schema_version VALUES(99,'0099_future',1)`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(tctx(t), dir, Options{}); !errors.Is(err, sqlitex.ErrSchemaTooNew) {
		t.Fatalf("err = %v", err)
	}
}

func TestMeta(t *testing.T) {
	ctx := tctx(t)
	s := newStore(t)
	if _, err := s.Meta(ctx, "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	for _, v := range []string{"1", "2"} {
		if err := s.SetMeta(ctx, "k", v); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Meta(ctx, "k"); got != v {
			t.Errorf("got %q want %q", got, v)
		}
	}
}

func TestCreateJob(t *testing.T) {
	ctx := tctx(t)
	s, _ := newStoreClock(t, t.TempDir())
	j, err := s.CreateJob(ctx, NewJob{Stage: "worker", Role: "default", Issue: 7, Data: json.RawMessage(`{"a":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(j.ID) != 26 || j.Status != StatusRunning || j.Attempt != 0 || !j.CreatedAt.Equal(t0) || j.PR != 0 {
		t.Errorf("job = %+v", j)
	}
	evs, _ := s.Events(ctx, EventFilter{})
	if len(evs) != 1 || evs[0].Kind != EventClaim || evs[0].JobID != j.ID || string(evs[0].Data) != `{"a":1}` {
		t.Errorf("events = %+v", evs)
	}
	if _, err := s.CreateJob(ctx, NewJob{Stage: "worker", Role: "default", Issue: 7}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second claim err = %v", err)
	}
	if evs, _ := s.Events(ctx, EventFilter{}); len(evs) != 1 {
		t.Errorf("failed claim left %d events", len(evs))
	}
	if _, err := s.CreateJob(ctx, NewJob{Stage: "worker", Role: "other", Issue: 7}); err != nil {
		t.Errorf("different role: %v", err)
	}
	if err := s.Complete(ctx, j.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateJob(ctx, NewJob{Stage: "worker", Role: "default", Issue: 7}); err != nil {
		t.Errorf("claim after complete: %v", err)
	}
}

func TestCreateJobValidation(t *testing.T) {
	ctx := tctx(t)
	s := newStore(t)
	for name, n := range map[string]NewJob{
		"bad stage":       {Stage: "merge", Role: "a", Issue: 1},
		"empty role":      {Stage: "worker", Role: "", Issue: 1},
		"bad role":        {Stage: "worker", Role: "_x", Issue: 1},
		"scanner issue":   {Stage: "scanner", Role: "cve", Issue: 3},
		"triage no issue": {Stage: "triage", Role: "t", Issue: 0},
		"bad data":        {Stage: "scanner", Role: "cve", Data: json.RawMessage(`{`)},
	} {
		if _, err := s.CreateJob(ctx, n); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, err := s.CreateJob(ctx, NewJob{Stage: "scanner", Role: "cve"}); err != nil {
		t.Errorf("scanner job: %v", err)
	}
}

func TestJobLookups(t *testing.T) {
	ctx := tctx(t)
	s, c := newStoreClock(t, t.TempDir())
	if _, err := s.Job(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Job err = %v", err)
	}
	if _, err := s.RunningJob(ctx, "worker", "a", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("RunningJob err = %v", err)
	}
	var ids []string
	for i := 1; i <= 3; i++ {
		c.add(time.Second)
		j, err := s.CreateJob(ctx, NewJob{Stage: "triage", Role: "t", Issue: i})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	_ = s.Complete(ctx, ids[0], nil)
	_ = s.Fail(ctx, ids[1], "boom", time.Time{})
	run, err := s.RunningJobs(ctx)
	if err != nil || len(run) != 1 || run[0].ID != ids[2] {
		t.Fatalf("running = %+v err=%v", run, err)
	}
	if j, err := s.RunningJob(ctx, "triage", "t", 3); err != nil || j.ID != ids[2] {
		t.Errorf("RunningJob = %+v err=%v", j, err)
	}
}

func TestHandleAndCloseout(t *testing.T) {
	ctx := tctx(t)
	s := newStore(t)
	j, _ := s.CreateJob(ctx, NewJob{Stage: "worker", Role: "a", Issue: 1, PR: 5})
	if err := s.SetHandle(ctx, j.ID, json.RawMessage(`{"pid":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCloseout(ctx, j.ID, "push", json.RawMessage(`{"x":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCloseout(ctx, j.ID, "label", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Job(ctx, j.ID)
	if string(got.Handle) != `{"pid":1}` || got.CloseoutPhase != "label" || string(got.CloseoutPlan) != `{"x":true}` || got.PR != 5 {
		t.Errorf("job = %+v", got)
	}
	if err := s.SetHandle(ctx, "nope", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if err := s.SetHandle(ctx, j.ID, json.RawMessage(`{`)); err == nil {
		t.Error("invalid JSON accepted")
	}
}

func TestAttempts(t *testing.T) {
	ctx := tctx(t)
	s, c := newStoreClock(t, t.TempDir())
	j, _ := s.CreateJob(ctx, NewJob{Stage: "worker", Role: "a", Issue: 1})
	for want := 1; want <= 2; want++ {
		n, err := s.StartAttempt(ctx, j.ID)
		if err != nil || n != want {
			t.Fatalf("n=%d err=%v", n, err)
		}
	}
	code, cost := 1, 0.5
	c.add(time.Minute)
	if err := s.EndAttempt(ctx, j.ID, 1, AttemptEnd{ExitCode: &code, Cause: "oom", Transient: true, CostUSD: &cost}); err != nil {
		t.Fatal(err)
	}
	if err := s.EndAttempt(ctx, j.ID, 9, AttemptEnd{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	as, _ := s.Attempts(ctx, j.ID)
	if len(as) != 2 || as[0].ExitCode == nil || *as[0].ExitCode != 1 || !as[0].Transient || as[0].Cause != "oom" ||
		*as[0].CostUSD != 0.5 || !as[0].EndedAt.Equal(t0.Add(time.Minute)) || as[1].ExitCode != nil || !as[1].EndedAt.IsZero() {
		t.Errorf("attempts = %+v", as)
	}
	if got, _ := s.Job(ctx, j.ID); got.Attempt != 2 {
		t.Errorf("job.Attempt = %d", got.Attempt)
	}
	if _, err := s.StartAttempt(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	_ = s.Complete(ctx, j.ID, nil)
	if _, err := s.StartAttempt(ctx, j.ID); !errors.Is(err, ErrNotRunning) {
		t.Errorf("err = %v", err)
	}
}

func TestTerminalTransitions(t *testing.T) {
	ctx := tctx(t)
	s, c := newStoreClock(t, t.TempDir())
	cost := 1.25
	keep := t0.Add(24 * time.Hour).In(time.FixedZone("x", 7200))

	done, _ := s.CreateJob(ctx, NewJob{Stage: "worker", Role: "a", Issue: 1})
	c.add(time.Minute)
	if err := s.Complete(ctx, done.ID, &cost); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, done.ID, nil); err != nil {
		t.Errorf("repeat Complete: %v", err)
	}
	if err := s.Fail(ctx, done.ID, "x", time.Time{}); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Fail after Complete: %v", err)
	}
	if err := s.Complete(ctx, "nope", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	got, _ := s.Job(ctx, done.ID)
	if got.Status != StatusDone || got.CostUSD == nil || *got.CostUSD != 1.25 || !got.EndedAt.Equal(t0.Add(time.Minute)) || !got.KeepUntil.IsZero() {
		t.Errorf("done job = %+v", got)
	}

	failed, _ := s.CreateJob(ctx, NewJob{Stage: "worker", Role: "a", Issue: 2})
	if err := s.Fail(ctx, failed.ID, "claim label failed", keep); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(ctx, failed.ID, "claim label failed", keep); err != nil {
		t.Errorf("repeat Fail: %v", err)
	}
	if err := s.Complete(ctx, failed.ID, nil); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Complete after Fail: %v", err)
	}
	got, _ = s.Job(ctx, failed.ID)
	if got.Status != StatusFailed || got.FailReason != "claim label failed" || !got.KeepUntil.Equal(keep) || got.KeepUntil.Location() != time.UTC || got.CostUSD != nil {
		t.Errorf("failed job = %+v", got)
	}
	for kind, n := range map[string]int{EventClaim: 2, EventCloseout: 1, EventFailure: 1} {
		evs, _ := s.Events(ctx, EventFilter{Kind: kind})
		if len(evs) != n {
			t.Errorf("%s events = %d, want %d", kind, len(evs), n)
		}
	}
}

func TestRestartKeepsRows(t *testing.T) {
	ctx := tctx(t)
	dir := t.TempDir()
	s, _ := newStoreClock(t, dir)
	j, _ := s.CreateJob(ctx, NewJob{Stage: "review", Role: "r", Issue: 4})
	_, _ = s.StartAttempt(ctx, j.ID)
	_ = s.SetMeta(ctx, "k", "v")
	s.Close()
	s2, _ := newStoreClock(t, dir)
	if got, err := s2.Job(ctx, j.ID); err != nil || got.Attempt != 1 {
		t.Errorf("job = %+v err=%v", got, err)
	}
	if as, _ := s2.Attempts(ctx, j.ID); len(as) != 1 {
		t.Errorf("attempts = %d", len(as))
	}
	if v, _ := s2.Meta(ctx, "k"); v != "v" {
		t.Errorf("meta = %q", v)
	}
}

func TestEvents(t *testing.T) {
	ctx := tctx(t)
	s, c := newStoreClock(t, t.TempDir())
	for i, k := range []string{EventRetry, EventTickError, EventRetry, EventRetry} {
		c.add(time.Minute)
		if err := s.AppendEvent(ctx, Event{Kind: k, Issue: i}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AppendEvent(ctx, Event{Kind: EventRetry, Data: json.RawMessage(`{`)}); err == nil {
		t.Error("invalid JSON accepted")
	}
	if err := s.AppendEvent(ctx, Event{}); err == nil {
		t.Error("empty kind accepted")
	}
	for name, tc := range map[string]struct {
		f    EventFilter
		want []int
	}{
		"all":      {EventFilter{}, []int{0, 1, 2, 3}},
		"kind":     {EventFilter{Kind: EventRetry}, []int{0, 2, 3}},
		"since":    {EventFilter{Since: t0.Add(3 * time.Minute)}, []int{2, 3}},
		"until":    {EventFilter{Until: t0.Add(2 * time.Minute)}, []int{0, 1}},
		"limit":    {EventFilter{Kind: EventRetry, Limit: 2}, []int{0, 2}},
		"no match": {EventFilter{Kind: "zzz"}, nil},
	} {
		evs, err := s.Events(ctx, tc.f)
		if err != nil {
			t.Fatal(err)
		}
		var got []int
		for _, e := range evs {
			got = append(got, e.Issue)
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v want %v", name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: got %v want %v", name, got, tc.want)
			}
		}
	}
}

func TestOpenCloseouts(t *testing.T) {
	ctx := tctx(t)
	s := newStore(t)
	mk := func(role, phase string) string {
		j, err := s.CreateJob(ctx, NewJob{Stage: "triage", Role: role, Issue: 1})
		if err != nil {
			t.Fatal(err)
		}
		if phase != "-" {
			if err := s.SetCloseout(ctx, j.ID, phase, json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
		}
		return j.ID
	}
	mk("none", "-")
	mk("empty", "")
	b := mk("prep", "prepared")
	mk("closed", "closed")
	got, err := s.OpenCloseouts(ctx)
	if err != nil || len(got) != 1 || got[0].ID != b {
		t.Fatalf("OpenCloseouts = %+v, %v", got, err)
	}
}
