package scannerdb

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/state/sqlitex"
)

var update = flag.Bool("update", false, "rewrite golden files")

var t0 = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func tctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func newDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(tctx(t), t.TempDir(), "cve", Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func fnd(fp string, issue int, first time.Time) Finding {
	return Finding{Fingerprint: fp, Issue: issue, Status: StatusOpen, Severity: "high", Group: "g", FirstSeen: first, LastSeen: first, LastSHA: "abc"}
}

func TestGoldenSchema(t *testing.T) {
	ctx := tctx(t)
	d := newDB(t)
	rows, err := d.db.QueryContext(ctx, `SELECT sql FROM sqlite_master WHERE sql IS NOT NULL ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		b.WriteString(s + ";\n")
	}
	if *update {
		if err := os.WriteFile("testdata/schema.golden", []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/schema.golden")
	if err != nil {
		t.Fatal(err)
	}
	if b.String() != string(want) {
		t.Errorf("schema changed; add a new migration instead of editing a released one (or run -update)\n%s", b.String())
	}
}

func TestPath(t *testing.T) {
	for name, ok := range map[string]bool{"cve": true, "stale-2": true, "": false, "../x": false, "a/b": false, "Cve": false, "_controller": false, ".x": false} {
		_, err := Path("/d", name)
		if (err == nil) != ok {
			t.Errorf("Path(%q) err = %v", name, err)
		}
	}
}

func TestApply(t *testing.T) {
	ctx := tctx(t)
	d := newDB(t)
	if err := d.Apply(ctx, Batch{PutFindings: []Finding{fnd("a", 1, t0), fnd("b", -3, t0)}}); err != nil {
		t.Fatal(err)
	}
	// upsert keeps first_seen
	up := fnd("a", 2, t0.Add(time.Hour))
	up.Severity = "low"
	if err := d.Apply(ctx, Batch{PutFindings: []Finding{up}}); err != nil {
		t.Fatal(err)
	}
	got, err := d.Finding(ctx, "a")
	if err != nil || !got.FirstSeen.Equal(t0) || !got.LastSeen.Equal(t0.Add(time.Hour)) || got.Issue != 2 || got.Severity != "low" {
		t.Errorf("a = %+v err=%v", got, err)
	}
	if fs, _ := d.FindingsForIssue(ctx, -3); len(fs) != 1 || fs[0].Fingerprint != "b" {
		t.Errorf("negative issue: %+v", fs)
	}
	// invalid row rejects the whole batch
	for name, bad := range map[string]Finding{
		"empty fp":   fnd("", 1, t0),
		"no seen":    {Fingerprint: "z", Status: StatusOpen},
		"bad status": {Fingerprint: "z", Status: "weird", FirstSeen: t0, LastSeen: t0},
	} {
		err := d.Apply(ctx, Batch{DeleteFindings: []string{"a"}, PutFindings: []Finding{fnd("new", 9, t0), bad}})
		if err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if all, _ := d.Findings(ctx); len(all) != 2 {
		t.Errorf("findings after rejected batches = %+v", all)
	}
	// delete
	if err := d.Apply(ctx, Batch{DeleteFindings: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Finding(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestSuppressions(t *testing.T) {
	ctx := tctx(t)
	d := newDB(t)
	expired := Suppression{Fingerprint: "s", Until: t0.Add(-time.Hour), Severity: "high", Issue: 4, Reason: "stale"}
	if err := d.Apply(ctx, Batch{PutSuppressions: []Suppression{expired}}); err != nil {
		t.Fatal(err)
	}
	got, err := d.Suppression(ctx, "s")
	if err != nil || got != expired {
		t.Errorf("got %+v err=%v", got, err)
	}
	if all, _ := d.Suppressions(ctx); len(all) != 1 {
		t.Errorf("all = %+v", all)
	}
	if err := d.Apply(ctx, Batch{PutSuppressions: []Suppression{{Fingerprint: "x"}}}); err == nil {
		t.Error("zero until accepted")
	}
	if err := d.Apply(ctx, Batch{DeleteSuppressions: []string{"s"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Suppression(ctx, "s"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestReplaceKeepsRunsAndMeta(t *testing.T) {
	ctx := tctx(t)
	d := newDB(t)
	_ = d.Apply(ctx, Batch{PutFindings: []Finding{fnd("a", 1, t0)}, PutSuppressions: []Suppression{{Fingerprint: "s", Until: t0}}})
	_, _ = d.RecordRun(ctx, Run{StartedAt: t0, Outcome: "ok"})
	_ = d.SetMeta(ctx, "k", "v")
	if err := d.Replace(ctx, []Finding{fnd("n", 2, t0)}, nil); err != nil {
		t.Fatal(err)
	}
	fs, _ := d.Findings(ctx)
	ss, _ := d.Suppressions(ctx)
	if len(fs) != 1 || fs[0].Fingerprint != "n" || len(ss) != 0 {
		t.Errorf("fs=%+v ss=%+v", fs, ss)
	}
	if _, err := d.LastRun(ctx); err != nil {
		t.Errorf("run lost: %v", err)
	}
	if v, _ := d.Meta(ctx, "k"); v != "v" {
		t.Errorf("meta = %q", v)
	}
	if err := d.Replace(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	if fs, _ := d.Findings(ctx); len(fs) != 0 {
		t.Errorf("fs = %+v", fs)
	}
	if err := d.Replace(ctx, []Finding{{Fingerprint: "bad"}}, nil); err == nil {
		t.Error("invalid replace accepted")
	}
}

func TestRuns(t *testing.T) {
	ctx := tctx(t)
	d := newDB(t)
	if _, err := d.LastRun(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := d.RecordRun(ctx, Run{StartedAt: t0.Add(time.Hour), Outcome: "ok", Found: 3}); err != nil {
		t.Fatal(err)
	}
	id2, _ := d.RecordRun(ctx, Run{StartedAt: t0, FinishedAt: t0.Add(time.Minute), SHA: "s", Outcome: "timeout"})
	got, err := d.LastRun(ctx)
	if err != nil || got.Found != 3 || got.Outcome != "ok" || got.ID == id2 {
		t.Errorf("last = %+v err=%v", got, err)
	}
	id3, _ := d.RecordRun(ctx, Run{StartedAt: t0.Add(time.Hour), Outcome: "error"})
	if got, _ := d.LastRun(ctx); got.ID != id3 {
		t.Errorf("tie should pick latest id: %+v", got)
	}
	if _, err := d.RecordRun(ctx, Run{StartedAt: t0, Outcome: "weird"}); err == nil {
		t.Error("bad outcome accepted")
	}
	if _, err := d.Meta(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func rawFindings(t *testing.T, path string) []string {
	t.Helper()
	db, err := sqlitex.Open(tctx(t), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.QueryContext(tctx(t), `SELECT fingerprint || ':' || issue FROM findings ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func TestIsolation(t *testing.T) {
	ctx := tctx(t)
	dir := t.TempDir()
	r := NewRegistry(dir, Options{})
	cve, err := r.Get(ctx, "cve")
	if err != nil {
		t.Fatal(err)
	}
	lint, _ := r.Get(ctx, "lint")
	if again, _ := r.Get(ctx, "cve"); again != cve {
		t.Error("Get returned a different handle")
	}
	_ = cve.Apply(ctx, Batch{PutFindings: []Finding{fnd("fp", 1, t0)}})
	_ = lint.Apply(ctx, Batch{PutFindings: []Finding{fnd("fp", 2, t0)}})
	if got := rawFindings(t, filepath.Join(dir, "cve.db")); len(got) != 1 || got[0] != "fp:1" {
		t.Errorf("cve.db = %v", got)
	}
	if got := rawFindings(t, filepath.Join(dir, "lint.db")); len(got) != 1 || got[0] != "fp:2" {
		t.Errorf("lint.db = %v", got)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := r.Get(ctx, "cve"); err == nil {
		t.Error("Get after Close succeeded")
	}
	for _, suf := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(filepath.Join(dir, "cve.db"+suf))
	}
	r2 := NewRegistry(dir, Options{})
	defer r2.Close()
	cve2, err := r2.Get(ctx, "cve")
	if err != nil {
		t.Fatal(err)
	}
	if fs, _ := cve2.Findings(ctx); len(fs) != 0 {
		t.Errorf("recreated cve.db not empty: %+v", fs)
	}
	lint2, _ := r2.Get(ctx, "lint")
	if fs, _ := lint2.Findings(ctx); len(fs) != 1 || fs[0].Issue != 2 {
		t.Errorf("lint.db damaged: %+v", fs)
	}
}

func TestConcurrentWrites(t *testing.T) {
	ctx := tctx(t)
	dir := t.TempDir()
	r := NewRegistry(dir, Options{})
	defer r.Close()
	cve, _ := r.Get(ctx, "cve")
	lint, _ := r.Get(ctx, "lint")
	other, err := Open(ctx, dir, "cve", Options{}) // second process stand-in (rebuild-state)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	write := func(d *DB, prefix string, n int) {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := d.Apply(ctx, Batch{PutFindings: []Finding{fnd(fmt.Sprintf("%s-%d", prefix, i), i+1, t0)}}); err != nil {
				errs <- err
				return
			}
		}
	}
	wg.Add(4)
	go write(cve, "c1", 100)
	go write(cve, "c2", 100)
	go write(lint, "l1", 100)
	go write(other, "o", 100)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if fs, _ := cve.Findings(ctx); len(fs) != 300 {
		t.Errorf("cve findings = %d, want 300", len(fs))
	}
	if fs, _ := lint.Findings(ctx); len(fs) != 100 {
		t.Errorf("lint findings = %d, want 100", len(fs))
	}
}

func TestConcurrentOpenNewFile(t *testing.T) {
	ctx := tctx(t)
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := Open(ctx, dir, "cve", Options{})
			if err != nil {
				errs <- err
				return
			}
			d.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestGoneStatusAndPipelineOutcomes(t *testing.T) {
	ctx := tctx(t)
	d := newDB(t)
	g := fnd("a", 1, t0)
	g.Status = StatusGone
	if err := d.Apply(ctx, Batch{PutFindings: []Finding{g}}); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Finding(ctx, "a"); err != nil || got.Status != StatusGone {
		t.Errorf("got %+v, %v", got, err)
	}
	for _, o := range []string{"ok", "dropped", "dropped_backpressure", "dropped_global_cap", "scan_failed", "timeout", "error"} {
		if _, err := d.RecordRun(ctx, Run{StartedAt: t0, Outcome: o}); err != nil {
			t.Errorf("outcome %q rejected: %v", o, err)
		}
	}
}
