package effects

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

// failClient serves reads from the fake and fails the test on any write.
type failClient struct {
	github.Reader
	t testing.TB
}

func (c failClient) fail(op string) { c.t.Helper(); c.t.Fatalf("dry-run wrote to GitHub: %s", op) }

func (c failClient) CreateIssue(context.Context, github.NewIssue) (github.Issue, error) {
	c.fail("CreateIssue")
	return github.Issue{}, nil
}
func (c failClient) UpdateIssue(context.Context, int, github.IssueUpdate) error {
	c.fail("UpdateIssue")
	return nil
}
func (c failClient) PinIssue(context.Context, int) error { c.fail("PinIssue"); return nil }
func (c failClient) AddLabels(context.Context, int, ...string) error {
	c.fail("AddLabels")
	return nil
}
func (c failClient) RemoveLabel(context.Context, int, string) error {
	c.fail("RemoveLabel")
	return nil
}
func (c failClient) CreateLabel(context.Context, github.Label) error {
	c.fail("CreateLabel")
	return nil
}
func (c failClient) UpdateLabel(context.Context, string, github.Label) error {
	c.fail("UpdateLabel")
	return nil
}
func (c failClient) CreateComment(context.Context, int, string) (github.Comment, error) {
	c.fail("CreateComment")
	return github.Comment{}, nil
}
func (c failClient) CreatePR(context.Context, github.NewPR) (github.PullRequest, error) {
	c.fail("CreatePR")
	return github.PullRequest{}, nil
}
func (c failClient) UpdatePR(context.Context, int, github.PRUpdate) error {
	c.fail("UpdatePR")
	return nil
}
func (c failClient) MarkReady(context.Context, int) error { c.fail("MarkReady"); return nil }
func (c failClient) CreateReview(context.Context, int, github.ReviewInput) (int64, error) {
	c.fail("CreateReview")
	return 0, nil
}
func (c failClient) UpdateBranch(context.Context, int, string) error {
	c.fail("UpdateBranch")
	return nil
}
func (c failClient) MergePR(context.Context, int, github.MergeInput) (github.MergeResult, error) {
	c.fail("MergePR")
	return github.MergeResult{}, nil
}
func (c failClient) DeleteBranch(context.Context, string) error { c.fail("DeleteBranch"); return nil }

var _ github.Client = failClient{}

func jsonLog() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

var author = gitx.Identity{Name: "ard", Email: "ard@example.com"}

func TestDryRunPerformsZeroWrites(t *testing.T) {
	ctx := context.Background()
	bare, clone := gittest.NewRemote(t)
	g := gitx.New("")
	fx, _, err := Open(ctx, Options{DryRun: true, Client: failClient{Reader: fake.New(), t: t}, Git: g, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	if !fx.DryRun() {
		t.Fatal("DryRun() = false")
	}

	// Every Writer method, discovered by reflection so a new one is covered.
	wt := reflect.TypeOf((*github.Writer)(nil)).Elem()
	for i := 0; i < wt.NumMethod(); i++ {
		m := wt.Method(i)
		in := []reflect.Value{reflect.ValueOf(ctx)}
		for j := 1; j < m.Type.NumIn(); j++ {
			if m.Type.IsVariadic() && j == m.Type.NumIn()-1 {
				break
			}
			in = append(in, reflect.Zero(m.Type.In(j)))
		}
		out := reflect.ValueOf(fx).MethodByName(m.Name).Call(in)
		if last := out[len(out)-1]; !last.IsNil() {
			t.Errorf("%s: %v", m.Name, last.Interface())
		}
	}

	headBefore := gittest.Run(t, clone, "rev-parse", "HEAD")
	refsBefore := gittest.Run(t, bare, "for-each-ref")
	if err := os.WriteFile(filepath.Join(clone, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "add", "f.txt")
	called := false
	res, err := fx.Commit(ctx, CommitOp{Dir: clone, Message: "m", Author: author})
	if err != nil || !res.Synthetic || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(res.SHA) {
		t.Fatalf("Commit = %+v, %v", res, err)
	}
	err = fx.Push(ctx, PushOp{Dir: clone, Remote: "origin", Ref: "HEAD:refs/heads/main",
		Token: func(context.Context) (string, error) { called = true; return "t", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("Token was called in dry-run")
	}
	if got := gittest.Run(t, clone, "rev-parse", "HEAD"); got != headBefore {
		t.Errorf("HEAD moved: %s -> %s", headBefore, got)
	}
	if got := gittest.Run(t, bare, "for-each-ref"); got != refsBefore {
		t.Errorf("remote refs changed:\n%s\n%s", refsBefore, got)
	}
	if got := strings.TrimSpace(gittest.Run(t, clone, "diff", "--cached", "--name-only")); got != "f.txt" {
		t.Errorf("index changed: %q", got)
	}
}

func TestDryRunCommitNothingStaged(t *testing.T) {
	_, clone := gittest.NewRemote(t)
	fx, _, err := Open(context.Background(), Options{DryRun: true, Client: fake.New()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Commit(context.Background(), CommitOp{Dir: clone, Message: "m", Author: author}); !errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("err = %v", err)
	}
}

func TestDryRunPayloads(t *testing.T) {
	ctx := context.Background()
	body, title := "secret body", "secret title"
	redact := func(s string) string { return strings.ReplaceAll(s, "secret", "XXX") }
	states := github.StateClosed
	reason := github.ReasonNotPlanned
	cases := []struct {
		name string
		call func(d *DryRun) error
		want []string
	}{
		{"CreateIssue", func(d *DryRun) error {
			is, err := d.CreateIssue(ctx, github.NewIssue{Title: title, Body: body, Labels: []string{"ard:a"}})
			if err == nil && (is.Number != -1 || is.URL != "dry-run://issue/-1" || is.Body != body) {
				return fmt.Errorf("bad result %+v", is)
			}
			return err
		}, []string{`"op":"CreateIssue"`, `"dry_run":true`, "XXX title", "XXX body", "ard:a"}},
		{"CreateComment", func(d *DryRun) error {
			c, err := d.CreateComment(ctx, 5, body)
			if err == nil && c.ID >= 0 {
				return fmt.Errorf("id %d", c.ID)
			}
			return err
		}, []string{`"op":"CreateComment"`, `"target":"#5"`, "XXX body"}},
		{"CreatePR", func(d *DryRun) error {
			pr, err := d.CreatePR(ctx, github.NewPR{Title: title, Body: body, Head: "h", Base: "main"})
			if err == nil && (pr.Number >= 0 || !strings.HasPrefix(pr.URL, "dry-run://pull/")) {
				return fmt.Errorf("bad result %+v", pr)
			}
			return err
		}, []string{`"op":"CreatePR"`, `"head":"h"`, "XXX title"}},
		{"UpdateIssue", func(d *DryRun) error {
			return d.UpdateIssue(ctx, 3, github.IssueUpdate{Title: &title, State: &states, StateReason: &reason})
		}, []string{"XXX title", `"state":"closed"`, `"state_reason":"not_planned"`}},
		{"UpdateIssueBody", func(d *DryRun) error { return d.UpdateIssueBody(ctx, 3, body) }, []string{`"op":"UpdateIssue"`, "XXX body"}},
		{"AddLabels", func(d *DryRun) error { return d.AddLabels(ctx, 3, "ard:x", "ard:y") }, []string{"ard:x", "ard:y"}},
		{"RemoveLabel", func(d *DryRun) error { return d.RemoveLabel(ctx, 3, "ard:x") }, []string{`"label":"ard:x"`}},
		{"CreateLabel", func(d *DryRun) error { return d.CreateLabel(ctx, github.Label{Name: "ard:n", Color: "aabbcc"}) }, []string{"ard:n", "aabbcc"}},
		{"PinIssue", func(d *DryRun) error { return d.PinIssue(ctx, 3) }, []string{`"op":"PinIssue"`}},
		{"CreateReview", func(d *DryRun) error {
			_, err := d.CreateReview(ctx, 3, github.ReviewInput{Body: body, Comments: []github.InlineComment{{Path: "p.go", Line: 2, Body: body}}})
			return err
		}, []string{"XXX body", "p.go"}},
		{"MergePR", func(d *DryRun) error {
			r, err := d.MergePR(ctx, 3, github.MergeInput{SHA: "abc", Message: body})
			if err == nil && r != (github.MergeResult{}) {
				return fmt.Errorf("merge result %+v", r)
			}
			return err
		}, []string{`"sha":"abc"`, "XXX body"}},
		{"Push", func(d *DryRun) error {
			return d.Push(ctx, PushOp{Dir: "/d", Remote: "origin", Ref: "HEAD:refs/heads/x", ForceWithLease: "deadbeef"})
		}, []string{"HEAD:refs/heads/x", "deadbeef", "origin"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log, buf := jsonLog()
			d, err := NewDryRun(DryRunOptions{Log: log, Redact: redact})
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.call(d); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("log lacks %q:\n%s", w, out)
				}
			}
			if strings.Contains(out, "secret") {
				t.Errorf("unredacted text in log:\n%s", out)
			}
		})
	}
}

func TestDryRunCommitStable(t *testing.T) {
	_, clone := gittest.NewRemote(t)
	gittest.Run(t, clone, "commit", "--allow-empty", "-m", "x")
	if err := os.WriteFile(filepath.Join(clone, "a"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "add", "a")
	d, _ := NewDryRun(DryRunOptions{Log: slog.New(slog.DiscardHandler), Staged: gitx.New("").StagedPaths})
	a, err := d.Commit(context.Background(), CommitOp{Dir: clone, Message: "m"})
	b, _ := d.Commit(context.Background(), CommitOp{Dir: clone, Message: "m"})
	c, _ := d.Commit(context.Background(), CommitOp{Dir: clone, Message: "other"})
	if err != nil || a != b || a.SHA == c.SHA {
		t.Fatalf("a=%v b=%v c=%v err=%v", a, b, c, err)
	}
}

func TestDryRunCancelled(t *testing.T) {
	d, _ := NewDryRun(DryRunOptions{Log: slog.New(slog.DiscardHandler)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.CreateIssue(ctx, github.NewIssue{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestLedger(t *testing.T) {
	ctx := context.Background()
	quiet := slog.New(slog.DiscardHandler)
	newDR := func(t *testing.T, path string) (*DryRun, error) {
		return NewDryRun(DryRunOptions{Log: quiet, LedgerPath: path})
	}
	issue := func(t *testing.T, d *DryRun) int {
		t.Helper()
		is, err := d.CreateIssue(ctx, github.NewIssue{Title: "t", Body: "<!-- ard:fp=abc -->"})
		if err != nil {
			t.Fatal(err)
		}
		return is.Number
	}

	t.Run("counter resumes", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "effects.jsonl")
		d, _ := newDR(t, p)
		if a, b := issue(t, d), issue(t, d); a != -1 || b != -2 {
			t.Fatalf("got %d %d", a, b)
		}
		d.Close()
		d, _ = newDR(t, p)
		if c := issue(t, d); c != -3 {
			t.Fatalf("got %d", c)
		}
		d.Close()
		data, _ := os.ReadFile(p)
		if n := bytes.Count(data, []byte("\n")); n != 3 {
			t.Fatalf("%d lines", n)
		}
	})
	t.Run("every effect recorded", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "effects.jsonl")
		d, _ := newDR(t, p)
		_ = d.AddLabels(ctx, 4, "ard:x")
		d.Close()
		data, _ := os.ReadFile(p)
		var r record
		if err := json.Unmarshal(data, &r); err != nil || r.Op != "AddLabels" || r.Target != "#4" {
			t.Fatalf("%+v %v", r, err)
		}
	})
	t.Run("truncated tail tolerated and repaired", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "effects.jsonl")
		d, _ := newDR(t, p)
		issue(t, d)
		d.Close()
		f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
		f.WriteString(`{"ts":"2026-01-01T00:00:00Z","op":"CreateIss`)
		f.Close()
		d, err := newDR(t, p)
		if err != nil {
			t.Fatal(err)
		}
		if n := issue(t, d); n != -2 {
			t.Fatalf("got %d", n)
		}
		d.Close()
		data, _ := os.ReadFile(p)
		for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
			if !json.Valid(line) {
				t.Fatalf("invalid line %q", line)
			}
		}
	})
	t.Run("corrupt middle line", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "effects.jsonl")
		good := `{"ts":"2026-01-01T00:00:00Z","op":"CreateIssue","result":{"number":-1}}`
		os.WriteFile(p, []byte(good+"\nnot json\n"+good+"\n"), 0o600)
		if _, err := newDR(t, p); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("no path no file", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		d, _ := newDR(t, "")
		issue(t, d)
		if es, _ := os.ReadDir(dir); len(es) != 0 {
			t.Fatal("file written")
		}
	})
	t.Run("concurrent unique", func(t *testing.T) {
		d, _ := newDR(t, filepath.Join(t.TempDir(), "e.jsonl"))
		defer d.Close()
		var mu sync.Mutex
		seen := map[int]bool{}
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				n := issue(t, d)
				mu.Lock()
				defer mu.Unlock()
				if seen[n] {
					t.Errorf("duplicate %d", n)
				}
				seen[n] = true
			}()
		}
		wg.Wait()
		if len(seen) != 50 {
			t.Fatalf("%d unique", len(seen))
		}
	})
}

// recorder is a github.Writer that records calls and returns err.
type recorder struct {
	calls []string
	err   error
}

func (r *recorder) rec(op string, args ...any) error {
	r.calls = append(r.calls, fmt.Sprint(append([]any{op}, args...)...))
	return r.err
}
func (r *recorder) CreateIssue(_ context.Context, in github.NewIssue) (github.Issue, error) {
	return github.Issue{Number: 9, Title: in.Title}, r.rec("CreateIssue", in.Title)
}
func (r *recorder) UpdateIssue(_ context.Context, n int, u github.IssueUpdate) error {
	return r.rec("UpdateIssue", n, *u.Body)
}
func (r *recorder) PinIssue(_ context.Context, n int) error { return r.rec("PinIssue", n) }
func (r *recorder) AddLabels(_ context.Context, n int, l ...string) error {
	return r.rec("AddLabels", n, l)
}
func (r *recorder) RemoveLabel(_ context.Context, n int, l string) error {
	return r.rec("RemoveLabel", n, l)
}
func (r *recorder) CreateLabel(_ context.Context, l github.Label) error {
	return r.rec("CreateLabel", l.Name)
}
func (r *recorder) UpdateLabel(_ context.Context, n string, l github.Label) error {
	return r.rec("UpdateLabel", n, l.Color)
}
func (r *recorder) CreateComment(_ context.Context, n int, b string) (github.Comment, error) {
	return github.Comment{ID: 1}, r.rec("CreateComment", n, b)
}
func (r *recorder) CreatePR(_ context.Context, in github.NewPR) (github.PullRequest, error) {
	return github.PullRequest{Number: 8}, r.rec("CreatePR", in.Head)
}
func (r *recorder) UpdatePR(_ context.Context, n int, u github.PRUpdate) error {
	return r.rec("UpdatePR", n, *u.Title)
}
func (r *recorder) MarkReady(_ context.Context, n int) error { return r.rec("MarkReady", n) }
func (r *recorder) CreateReview(_ context.Context, n int, in github.ReviewInput) (int64, error) {
	return 5, r.rec("CreateReview", n, in.Body)
}
func (r *recorder) UpdateBranch(_ context.Context, n int, s string) error {
	return r.rec("UpdateBranch", n, s)
}
func (r *recorder) MergePR(_ context.Context, n int, in github.MergeInput) (github.MergeResult, error) {
	return github.MergeResult{SHA: "m"}, r.rec("MergePR", n, in.SHA)
}
func (r *recorder) DeleteBranch(_ context.Context, b string) error { return r.rec("DeleteBranch", b) }

func TestLiveForwarding(t *testing.T) {
	ctx := context.Background()
	s := "v"
	calls := []struct {
		name string
		want string
		call func(l *Live) error
	}{
		{"CreateIssue", "CreateIssuet", func(l *Live) error { _, e := l.CreateIssue(ctx, github.NewIssue{Title: "t"}); return e }},
		{"UpdateIssue", "UpdateIssue3v", func(l *Live) error { return l.UpdateIssue(ctx, 3, github.IssueUpdate{Body: &s}) }},
		{"UpdateIssueBody", "UpdateIssue3v", func(l *Live) error { return l.UpdateIssueBody(ctx, 3, s) }},
		{"PinIssue", "PinIssue3", func(l *Live) error { return l.PinIssue(ctx, 3) }},
		{"AddLabels", "AddLabels3 [a b]", func(l *Live) error { return l.AddLabels(ctx, 3, "a", "b") }},
		{"RemoveLabel", "RemoveLabel3a", func(l *Live) error { return l.RemoveLabel(ctx, 3, "a") }},
		{"CreateLabel", "CreateLabelx", func(l *Live) error { return l.CreateLabel(ctx, github.Label{Name: "x"}) }},
		{"UpdateLabel", "UpdateLabelxfff", func(l *Live) error { return l.UpdateLabel(ctx, "x", github.Label{Color: "fff"}) }},
		{"CreateComment", "CreateComment3hi", func(l *Live) error { _, e := l.CreateComment(ctx, 3, "hi"); return e }},
		{"CreatePR", "CreatePRh", func(l *Live) error { _, e := l.CreatePR(ctx, github.NewPR{Head: "h"}); return e }},
		{"UpdatePR", "UpdatePR3v", func(l *Live) error { return l.UpdatePR(ctx, 3, github.PRUpdate{Title: &s}) }},
		{"MarkReady", "MarkReady3", func(l *Live) error { return l.MarkReady(ctx, 3) }},
		{"CreateReview", "CreateReview3b", func(l *Live) error { _, e := l.CreateReview(ctx, 3, github.ReviewInput{Body: "b"}); return e }},
		{"UpdateBranch", "UpdateBranch3sha", func(l *Live) error { return l.UpdateBranch(ctx, 3, "sha") }},
		{"MergePR", "MergePR3sha", func(l *Live) error { _, e := l.MergePR(ctx, 3, github.MergeInput{SHA: "sha"}); return e }},
		{"DeleteBranch", "DeleteBranchb", func(l *Live) error { return l.DeleteBranch(ctx, "b") }},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			l := NewLive(rec, gitx.New(""), slog.New(slog.DiscardHandler))
			if err := tc.call(l); err != nil {
				t.Fatal(err)
			}
			if len(rec.calls) != 1 || rec.calls[0] != tc.want {
				t.Fatalf("calls = %q, want %q", rec.calls, tc.want)
			}
			// Errors wrap and keep their identity.
			rec = &recorder{err: github.ErrNotFound}
			l = NewLive(rec, gitx.New(""), slog.New(slog.DiscardHandler))
			if err := tc.call(l); !errors.Is(err, github.ErrNotFound) || !strings.Contains(err.Error(), "effects: ") {
				t.Fatalf("err = %v", err)
			}
		})
	}

	t.Run("synthetic numbers rejected", func(t *testing.T) {
		for _, n := range []int{-1, 0} {
			rec := &recorder{}
			l := NewLive(rec, gitx.New(""), slog.New(slog.DiscardHandler))
			var errs []error
			errs = append(errs,
				l.UpdateIssue(ctx, n, github.IssueUpdate{}), l.UpdateIssueBody(ctx, n, "b"),
				l.PinIssue(ctx, n), l.AddLabels(ctx, n, "a"), l.RemoveLabel(ctx, n, "a"),
				l.UpdatePR(ctx, n, github.PRUpdate{}), l.MarkReady(ctx, n),
				l.UpdateBranch(ctx, n, "s"))
			_, e0 := l.CreateReview(ctx, n, github.ReviewInput{})
			errs = append(errs, e0)
			_, e1 := l.CreateComment(ctx, n, "b")
			_, e2 := l.MergePR(ctx, n, github.MergeInput{})
			errs = append(errs, e1, e2)
			for _, e := range errs {
				if !errors.Is(e, ErrSyntheticTarget) {
					t.Errorf("n=%d err = %v", n, e)
				}
			}
			if len(rec.calls) != 0 {
				t.Errorf("writer called: %v", rec.calls)
			}
		}
	})
}

func TestLiveGit(t *testing.T) {
	ctx := context.Background()
	bare, clone := gittest.NewRemote(t)
	l := NewLive(&recorder{}, gitx.New(""), slog.New(slog.DiscardHandler))
	if _, err := l.Commit(ctx, CommitOp{Dir: clone, Message: "m", Author: author}); !errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("err = %v", err)
	}
	os.WriteFile(filepath.Join(clone, "a"), []byte("a"), 0o644)
	gittest.Run(t, clone, "add", "a")
	res, err := l.Commit(ctx, CommitOp{Dir: clone, Message: "m", Author: author})
	if err != nil || res.Synthetic || len(res.SHA) != 40 {
		t.Fatalf("%+v %v", res, err)
	}
	if err := l.Push(ctx, PushOp{Dir: clone, Remote: "origin", Ref: "HEAD:refs/heads/main"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gittest.Run(t, bare, "rev-parse", "main")); got != res.SHA {
		t.Fatalf("remote %s want %s", got, res.SHA)
	}
}

func TestOpen(t *testing.T) {
	ctx := context.Background()
	for _, dry := range []bool{false, true} {
		fx, rd, err := Open(ctx, Options{DryRun: dry, Client: fake.New(), Log: slog.New(slog.DiscardHandler)})
		if err != nil {
			t.Fatal(err)
		}
		if fx.DryRun() != dry {
			t.Errorf("DryRun() = %v, want %v", fx.DryRun(), dry)
		}
		if _, ok := rd.(github.Writer); ok {
			t.Error("reader is assertable to github.Writer")
		}
		if _, ok := rd.(github.Client); ok {
			t.Error("reader is assertable to github.Client")
		}
		if _, err := rd.ListLabels(ctx); err != nil {
			t.Errorf("read through reader: %v", err)
		}
	}
	if _, _, err := Open(ctx, Options{}); err == nil {
		t.Error("Open without client or config should fail")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := Open(cctx, Options{Client: fake.New()}); err == nil {
		t.Error("cancelled ctx")
	}
}

func TestIsSynthetic(t *testing.T) {
	for n, want := range map[int]bool{-5: true, -1: true, 0: false, 7: false} {
		if IsSynthetic(n) != want {
			t.Errorf("IsSynthetic(%d)", n)
		}
	}
}

// faultFile injects write or sync failures into the ledger.
type faultFile struct {
	ledgerFile
	failWrite, failSync, failTruncate bool
}

func (f *faultFile) Write(b []byte) (int, error) {
	if f.failWrite {
		n, _ := f.ledgerFile.Write(b[:len(b)/2]) // partial write
		return n, errors.New("disk full")
	}
	return f.ledgerFile.Write(b)
}
func (f *faultFile) Sync() error {
	if f.failSync {
		return errors.New("fsync failed")
	}
	return f.ledgerFile.Sync()
}
func (f *faultFile) Truncate(n int64) error {
	if f.failTruncate {
		return errors.New("truncate failed")
	}
	return f.ledgerFile.Truncate(n)
}

func TestLedgerFaults(t *testing.T) {
	ctx := context.Background()
	for _, fault := range []string{"write", "sync"} {
		t.Run(fault, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "e.jsonl")
			d, _ := NewDryRun(DryRunOptions{Log: slog.New(slog.DiscardHandler), LedgerPath: p})
			ff := &faultFile{ledgerFile: d.ledger, failWrite: fault == "write", failSync: fault == "sync"}
			d.ledger = ff
			if _, err := d.CreateIssue(ctx, github.NewIssue{Title: "a"}); err == nil {
				t.Fatal("want error")
			}
			ff.failWrite, ff.failSync = false, false
			is, err := d.CreateIssue(ctx, github.NewIssue{Title: "b"})
			if err != nil {
				t.Fatal(err)
			}
			if is.Number != -2 {
				t.Errorf("number %d reissued or wrong, want -2", is.Number)
			}
			d.Close()
			data, _ := os.ReadFile(p)
			for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
				if !json.Valid(line) {
					t.Fatalf("corrupt line %q", line)
				}
			}
			if _, err := NewDryRun(DryRunOptions{Log: slog.New(slog.DiscardHandler), LedgerPath: p}); err != nil {
				t.Fatalf("ledger unreadable: %v", err)
			}
		})
	}
	t.Run("truncate fails", func(t *testing.T) {
		d, _ := NewDryRun(DryRunOptions{Log: slog.New(slog.DiscardHandler), LedgerPath: filepath.Join(t.TempDir(), "e.jsonl")})
		d.ledger = &faultFile{ledgerFile: d.ledger, failWrite: true, failTruncate: true}
		if _, err := d.CreateIssue(ctx, github.NewIssue{}); err == nil {
			t.Fatal("want error")
		}
		d.ledger = d.ledger.(*faultFile).ledgerFile
		if err := d.AddLabels(ctx, 1, "x"); err == nil || !strings.Contains(err.Error(), "unusable") {
			t.Fatalf("later effect err = %v", err)
		}
	})
}

func TestNewClientReadOnlyUnderDryRun(t *testing.T) {
	var reqs int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reqs++ }))
	defer srv.Close()
	for _, dry := range []bool{true} {
		c, err := newClient(Options{DryRun: dry, GitHub: github.Config{
			Repo: github.Repo{Owner: "o", Name: "r"}, Auth: github.NewTokenAuth("t"), BaseURL: srv.URL}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.CreateIssue(context.Background(), github.NewIssue{Title: "x"}); !errors.Is(err, github.ErrReadOnly) {
			t.Fatalf("err = %v, want ErrReadOnly", err)
		}
	}
	if reqs != 0 {
		t.Fatalf("server saw %d requests", reqs)
	}
}
