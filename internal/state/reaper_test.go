package state

import (
	"errors"
	"github.com/albertcbrown/ard/internal/state/sqlitex"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

func claim(t *testing.T, s *Store, role string, issue int) Job {
	t.Helper()
	j, err := s.CreateJob(tctx(t), NewJob{Stage: "worker", Role: role, Issue: issue, Branch: "worker/" + role + "-x"})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestCreateJobStoresBranch(t *testing.T) {
	s := newStore(t)
	j := claim(t, s, "eng", 5)
	if j.Branch != "worker/eng-x" || j.RetryOf != "" || j.AutoRetry || j.KillReason != "" {
		t.Errorf("job = %+v", j)
	}
}

func TestSetKillReason(t *testing.T) {
	ctx := tctx(t)
	s := newStore(t)
	j := claim(t, s, "eng", 5)
	if err := s.SetKillReason(ctx, j.ID, "heartbeat_quiet"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetKillReason(ctx, j.ID, "deadline"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Job(ctx, j.ID)
	if got.KillReason != "heartbeat_quiet" {
		t.Errorf("first reason must win, got %q", got.KillReason)
	}
	evs, _ := s.Events(ctx, EventFilter{Kind: EventKill})
	if len(evs) != 2 {
		t.Errorf("kill events = %d, want 2", len(evs))
	}
	if err := s.Fail(ctx, j.ID, "x", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetKillReason(ctx, j.ID, "deadline"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("ended job: err = %v, want ErrNotRunning", err)
	}
	if err := s.SetKillReason(ctx, "nope", "deadline"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing job: err = %v, want ErrNotFound", err)
	}
	if err := s.SetKillReason(ctx, j.ID, ""); err == nil {
		t.Error("empty reason accepted")
	}
}

func TestRetryLinkage(t *testing.T) {
	ctx := tctx(t)
	s := newStore(t)
	first := claim(t, s, "eng", 5)

	// Plain failure then a human retry: not linked.
	if err := s.Fail(ctx, first.ID, "agent exited 1", time.Time{}); err != nil {
		t.Fatal(err)
	}
	human := claim(t, s, "eng", 5)
	if human.RetryOf != "" || human.ID == first.ID {
		t.Fatalf("human retry = %+v", human)
	}

	// Automatic hand-off: the next claim is linked, the one after is not.
	if err := s.FailRetry(ctx, human.ID, "rate_limit", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	failed, _ := s.Job(ctx, human.ID)
	if failed.Status != StatusFailed || !failed.AutoRetry {
		t.Fatalf("failed = %+v", failed)
	}
	if evs, _ := s.Events(ctx, EventFilter{Kind: EventRetry}); len(evs) != 1 {
		t.Errorf("retry events = %d, want 1", len(evs))
	}
	auto := claim(t, s, "eng", 5)
	if auto.RetryOf != human.ID || auto.ID == human.ID {
		t.Fatalf("auto retry = %+v, want retry_of %s", auto, human.ID)
	}
	// A different issue or role is never linked.
	if o := claim(t, s, "eng", 6); o.RetryOf != "" {
		t.Errorf("other issue linked: %+v", o)
	}
	// The retry fails again (plain Fail): the next claim is a human retry.
	if err := s.Fail(ctx, auto.ID, "x", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if again := claim(t, s, "eng", 5); again.RetryOf != "" {
		t.Errorf("claim after a failed retry linked: %+v", again)
	}
}

func TestRetryLinkOnlyFirstClaim(t *testing.T) {
	ctx := tctx(t)
	s := newStore(t)
	a := claim(t, s, "eng", 5)
	if err := s.FailRetry(ctx, a.ID, "rate_limit", time.Time{}); err != nil {
		t.Fatal(err)
	}
	b := claim(t, s, "eng", 5)
	if b.RetryOf != a.ID {
		t.Fatalf("first claim not linked: %+v", b)
	}
	if err := s.Complete(ctx, b.ID, nil); err != nil {
		t.Fatal(err)
	}
	if c := claim(t, s, "eng", 5); c.RetryOf != "" {
		t.Errorf("second claim after one hand-off linked: %+v", c)
	}
}

func TestExpiredKeepClean(t *testing.T) {
	ctx := tctx(t)
	s, c := newStoreClock(t, t.TempDir())
	failed := claim(t, s, "eng", 1)
	done := claim(t, s, "eng", 2)
	running := claim(t, s, "eng", 3)
	if err := s.Fail(ctx, failed.ID, "x", c.t.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, done.ID, nil); err != nil { // no keep_until: ended_at + 24 h
		t.Fatal(err)
	}
	exp := func(at time.Time) map[string]bool {
		js, err := s.Expired(ctx, at, 10)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]bool{}
		for _, j := range js {
			m[j.ID] = true
		}
		return m
	}
	if m := exp(c.t.Add(24*time.Hour - time.Second)); len(m) != 0 {
		t.Errorf("not yet expired: %v", m)
	}
	m := exp(c.t.Add(24 * time.Hour))
	if !m[failed.ID] || !m[done.ID] || m[running.ID] || len(m) != 2 {
		t.Errorf("expired = %v", m)
	}
	if err := s.ExtendKeep(ctx, failed.ID, c.t.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if m := exp(c.t.Add(24 * time.Hour)); m[failed.ID] || !m[done.ID] {
		t.Errorf("after extend: %v", m)
	}
	if err := s.ExtendKeep(ctx, running.ID, c.t); !errors.Is(err, ErrNotFound) {
		t.Errorf("extend running: %v", err)
	}
	if err := s.MarkCleaned(ctx, done.ID); err != nil {
		t.Fatal(err)
	}
	c.add(time.Hour)
	if err := s.MarkCleaned(ctx, done.ID); err != nil { // idempotent, first wins
		t.Fatal(err)
	}
	got, _ := s.Job(ctx, done.ID)
	if got.CleanedAt.IsZero() || got.CleanedAt.Equal(c.t) {
		t.Errorf("cleaned_at = %v, first call must win", got.CleanedAt)
	}
	if m := exp(c.t.Add(72 * time.Hour)); m[done.ID] {
		t.Errorf("cleaned job still expired: %v", m)
	}
	if err := s.MarkCleaned(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("mark cleaned missing: %v", err)
	}
}

func TestEndedSince(t *testing.T) {
	ctx := tctx(t)
	s, c := newStoreClock(t, t.TempDir())
	a := claim(t, s, "eng", 1)
	if err := s.Complete(ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	c.add(time.Hour)
	b := claim(t, s, "eng", 2)
	if err := s.Fail(ctx, b.ID, "x", time.Time{}); err != nil {
		t.Fatal(err)
	}
	claim(t, s, "eng", 3) // still running
	js, err := s.EndedSince(ctx, c.t.Add(-time.Minute))
	if err != nil || len(js) != 1 || js[0].ID != b.ID {
		t.Errorf("EndedSince = %v, %v", js, err)
	}
}

// An existing 0001 database upgrades in place and reopens cleanly.
func TestUpgradeFromInit(t *testing.T) {
	ctx := tctx(t)
	dir := t.TempDir()
	init, err := fs.ReadFile(migrationsFS, "migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitex.Open(ctx, filepath.Join(dir, ControllerFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlitex.Migrate(ctx, db, fstest.MapFS{"0001_init.sql": {Data: init}}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO jobs(id, stage, role, issue, status, attempt, created_at, updated_at)
		VALUES('OLD','worker','eng',5,'failed',1,1,1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	versions := func(s *Store) int {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_version`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	s, err := Open(ctx, dir, Options{Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatal(err)
	}
	var max int
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&max); err != nil || max != 2 {
		t.Fatalf("schema version = %d, %v", max, err)
	}
	j, err := s.Job(ctx, "OLD")
	if err != nil || j.Branch != "" || j.RetryOf != "" || j.AutoRetry || j.KillReason != "" {
		t.Fatalf("old row = %+v, %v", j, err)
	}
	n := versions(s)
	s.Close()
	s2, err := Open(ctx, dir, Options{Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if versions(s2) != n {
		t.Errorf("reopen added schema_version rows")
	}
}
