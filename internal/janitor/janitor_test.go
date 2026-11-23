package janitor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/labels"
)

var t0 = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

type rec struct {
	reqs []committer.Request
	fail map[int]error
}

func (r *rec) Commit(_ context.Context, req committer.Request) (committer.Result, error) {
	if err := r.fail[req.Issue]; err != nil {
		return committer.Result{}, err
	}
	r.reqs = append(r.reqs, req)
	return committer.Result{SHA: "abc"}, nil
}

// reopener reports one issue as reopened on re-fetch.
type reopener struct {
	Reader
	n int
}

func (r reopener) GetIssue(ctx context.Context, n int) (github.Issue, error) {
	is, err := r.Reader.GetIssue(ctx, n)
	if n == r.n {
		is.State, is.ClosedAt = github.StateOpen, nil
	}
	return is, err
}

func closedAgo(days int, lbls ...string) github.Issue {
	c := t0.Add(-time.Duration(days) * 24 * time.Hour)
	return github.Issue{Title: "x", State: github.StateClosed, ClosedAt: &c, CreatedAt: c.Add(-time.Hour), Labels: lbls}
}

func env() controller.Env {
	return controller.Env{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func setup(t *testing.T, gh Reader, paths ...string) (*Janitor, *rec, *time.Time) {
	t.Helper()
	now := t0
	r := &rec{fail: map[int]error{}}
	j := New(gh, r, func(context.Context) ([]string, error) { return paths, nil }, func() time.Time { return now })
	return j, r, &now
}

func TestJanitorPrunesOldClosed(t *testing.T) {
	f := fake.New()
	n := f.SeedIssue(closedAgo(91))
	j, r, _ := setup(t, f, fmt.Sprintf(".ard/%d/a.md", n), fmt.Sprintf(".ard/%d/logs/x.log", n))
	if err := j.Tick(context.Background(), env()); err != nil {
		t.Fatal(err)
	}
	if len(r.reqs) != 1 || r.reqs[0].Issue != n || len(r.reqs[0].Files) != 2 {
		t.Fatalf("reqs = %+v", r.reqs)
	}
	for _, f := range r.reqs[0].Files {
		if !f.Delete {
			t.Errorf("%s not a delete", f.Path)
		}
	}
}

func TestJanitorKeepsRecent(t *testing.T) {
	f := fake.New()
	n := f.SeedIssue(closedAgo(89))
	j, r, _ := setup(t, f, fmt.Sprintf(".ard/%d/a.md", n))
	if err := j.Tick(context.Background(), env()); err != nil || len(r.reqs) != 0 {
		t.Fatalf("err %v reqs %v", err, r.reqs)
	}
}

func TestJanitorSkipsOpenAndReopened(t *testing.T) {
	f := fake.New()
	open := f.SeedIssue(github.Issue{Title: "o", State: github.StateOpen})
	re := f.SeedIssue(closedAgo(200))
	j, r, _ := setup(t, reopener{f, re}, fmt.Sprintf(".ard/%d/a.md", open), fmt.Sprintf(".ard/%d/a.md", re))
	if err := j.Tick(context.Background(), env()); err != nil || len(r.reqs) != 0 {
		t.Fatalf("err %v reqs %v", err, r.reqs)
	}
}

func TestJanitorNeverTouchesNonIssueDirs(t *testing.T) {
	f := fake.New()
	n := f.SeedIssue(closedAgo(100))
	j, r, _ := setup(t, f, ".ard/config.yaml", ".ard/triage/lead/x.md", ".ard/workers/engineer/y.md", ".ard/scanners/z", fmt.Sprintf(".ard/%d/a.md", n))
	if err := j.Tick(context.Background(), env()); err != nil {
		t.Fatal(err)
	}
	if len(r.reqs) != 1 || len(r.reqs[0].Files) != 1 || !strings.HasPrefix(r.reqs[0].Files[0].Path, fmt.Sprintf(".ard/%d/", n)) {
		t.Fatalf("reqs = %+v", r.reqs)
	}
}

func TestJanitorSkipsKeepAndHold(t *testing.T) {
	f := fake.New()
	a := f.SeedIssue(closedAgo(100, labels.Keep))
	b := f.SeedIssue(closedAgo(100, labels.Hold))
	j, r, _ := setup(t, f, fmt.Sprintf(".ard/%d/a.md", a), fmt.Sprintf(".ard/%d/a.md", b))
	if err := j.Tick(context.Background(), env()); err != nil || len(r.reqs) != 0 {
		t.Fatalf("err %v reqs %v", err, r.reqs)
	}
}

func TestJanitorChunksOver256Files(t *testing.T) {
	f := fake.New()
	n := f.SeedIssue(closedAgo(100))
	var paths []string
	for i := 0; i < 300; i++ {
		paths = append(paths, fmt.Sprintf(".ard/%d/f%03d.md", n, i))
	}
	j, r, _ := setup(t, f, paths...)
	if err := j.Tick(context.Background(), env()); err != nil {
		t.Fatal(err)
	}
	if len(r.reqs) != 2 || len(r.reqs[0].Files) != committer.MaxFiles || len(r.reqs[1].Files) != 44 || r.reqs[1].Issue != n {
		t.Fatalf("got %d reqs", len(r.reqs))
	}
}

func TestJanitorContinuesAfterCommitError(t *testing.T) {
	f := fake.New()
	a := f.SeedIssue(closedAgo(100))
	b := f.SeedIssue(closedAgo(100))
	j, r, _ := setup(t, f, fmt.Sprintf(".ard/%d/a.md", a), fmt.Sprintf(".ard/%d/a.md", b))
	r.fail[a] = errors.New("boom")
	if err := j.Tick(context.Background(), env()); err == nil {
		t.Fatal("want error")
	}
	if len(r.reqs) != 1 || r.reqs[0].Issue != b {
		t.Fatalf("reqs = %+v", r.reqs)
	}
}

func TestJanitorMonthlyGate(t *testing.T) {
	f := fake.New()
	n := f.SeedIssue(closedAgo(100))
	j, r, now := setup(t, f, fmt.Sprintf(".ard/%d/a.md", n))
	ctx := context.Background()
	_ = j.Tick(ctx, env())
	_ = j.Tick(ctx, env())
	*now = now.Add(29 * 24 * time.Hour)
	_ = j.Tick(ctx, env())
	if len(r.reqs) != 1 {
		t.Fatalf("within 30 days: %d commits", len(r.reqs))
	}
	*now = now.Add(2 * 24 * time.Hour)
	_ = j.Tick(ctx, env())
	if len(r.reqs) != 2 {
		t.Fatalf("after 30 days: %d commits", len(r.reqs))
	}
}

func TestJanitorClaimsAndName(t *testing.T) {
	j, _, _ := setup(t, fake.New())
	if !j.Claims() || j.Name() != "janitor" {
		t.Fatal("claims/name")
	}
}
