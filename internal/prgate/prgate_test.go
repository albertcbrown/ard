package prgate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

func TestParseWorkerBranch(t *testing.T) {
	tests := []struct {
		ref   string
		role  string
		issue int
		ok    bool
	}{
		{"worker/engineer-12-fix-bug", "engineer", 12, true},
		{"worker/v2-eng-7-2024-roadmap", "v2-eng", 7, true},
		{"worker/eng-12-9", "eng", 12, true},
		{"worker/eng-12", "", 0, false},
		{"worker/12-x", "", 0, false},
		{"worker/eng-0-x", "", 0, false},
		{"worker/eng--3-x", "", 0, false},
		{"worker/eng-3-", "", 0, false},
		{"feature/x", "", 0, false},
		{"worker/", "", 0, false},
		{"worker/eng-99999999999999999999-x", "", 0, false},
	}
	for _, tc := range tests {
		role, issue, ok := ParseWorkerBranch(tc.ref)
		if role != tc.role || issue != tc.issue || ok != tc.ok {
			t.Errorf("ParseWorkerBranch(%q) = %q, %d, %v; want %q, %d, %v", tc.ref, role, issue, ok, tc.role, tc.issue, tc.ok)
		}
	}
}

func TestLabels(t *testing.T) {
	roles := []string{"eng-review", "sec-review"}
	req := func(r string) string { return labels.Request(labels.StageReview, r) }
	done := func(r string) string { return labels.Done(labels.StageReview, r) }
	tests := []struct {
		name   string
		have   []string
		want   string // "" is OK
		nroles int
	}{
		{"one role done", []string{req("eng-review"), done("eng-review")}, "", 1},
		{"two roles done", []string{req("eng-review"), done("eng-review"), req("sec-review"), done("sec-review")}, "", 2},
		{"unrequested role ignored", []string{req("eng-review"), done("eng-review"), "other"}, "", 1},
		{"no reviews", []string{"x"}, ReasonNoReviews, 0},
		{"second pending", []string{req("eng-review"), done("eng-review"), req("sec-review")}, ReasonPending, 1},
		{"changes", []string{req("eng-review"), done("eng-review"), labels.Changes("eng-review")}, ReasonChanges, 0},
		{"running", []string{req("eng-review"), done("eng-review"), labels.Running(labels.StageReview, "eng-review")}, ReasonRunning, 0},
		{"needs-human", []string{req("eng-review"), done("eng-review"), labels.NeedsHuman}, ReasonBlocked, 0},
		{"hold", []string{req("eng-review"), done("eng-review"), labels.Hold}, ReasonBlocked, 0},
		{"tests failed", []string{req("eng-review"), done("eng-review"), labels.TestsFailed}, ReasonBlocked, 0},
		{"executor failed", []string{req("eng-review"), done("eng-review"), labels.ExecutorFailed}, ReasonBlocked, 0},
		{"case-insensitive", []string{strings.ToUpper(req("eng-review")), strings.ToUpper(done("eng-review"))}, "", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := Labels(tc.have, roles)
			if v.OK != (tc.want == "") || v.Reason != tc.want {
				t.Fatalf("verdict = %+v, want reason %q", v, tc.want)
			}
			if v.OK && len(v.Roles) != tc.nroles {
				t.Errorf("roles = %v", v.Roles)
			}
		})
	}
}

func TestIsIssuePath(t *testing.T) {
	for p, want := range map[string]bool{
		".ard/12/plan.md":       true,
		".ard/7/logs/a.log":     true,
		".ard/config.yaml":      false,
		".ard/workers/eng/x.md": false,
		".ard/12":               false,
		".ard/1a/x":             false,
		"a/.ard/12/x":           false,
		"internal/x.go":         false,
		".ard//x":               false,
		".ard/12x/y":            false,
		".ard/reviewers/r/12/x": false,
	} {
		if got := IsIssuePath(p); got != want {
			t.Errorf("IsIssuePath(%q) = %v", p, got)
		}
	}
}

// fakeGit answers CodeChanged by (from, to).
type fakeGit struct {
	diffs map[[2]string][]string
	err   error
}

func (g fakeGit) CodeChanged(_ context.Context, _, from, to string, _ ...string) ([]string, error) {
	if g.err != nil {
		return nil, g.err
	}
	return g.diffs[[2]string{from, to}], nil
}

func TestCovers(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name     string
		reviewed string
		base     string
		diffs    map[[2]string][]string
		err      error
		want     bool
		broken   []string
		wantErr  bool
	}{
		{name: "same head", reviewed: "x", want: true},
		{name: "empty reviewed", reviewed: "", want: false},
		{name: "only issue dir", reviewed: "x", diffs: map[[2]string][]string{{"x", "y"}: {".ard/3/worker-eng.md", ".ard/3/logs/a"}}, want: true},
		{name: "other issue dir is not tolerated", reviewed: "x", diffs: map[[2]string][]string{{"x", "y"}: {".ard/9/plan.md"}}, want: false, broken: []string{".ard/9/plan.md"}},
		{name: "other issue dir from main is tolerated", reviewed: "x", base: "b", diffs: map[[2]string][]string{{"x", "y"}: {".ard/9/plan.md"}}, want: true},
		{name: "code, no base", reviewed: "x", diffs: map[[2]string][]string{{"x", "y"}: {"a.go"}}, want: false, broken: []string{"a.go"}},
		{name: "config is code", reviewed: "x", diffs: map[[2]string][]string{{"x", "y"}: {".ard/config.yaml"}}, want: false, broken: []string{".ard/config.yaml"}},
		{name: "main-sourced path tolerated", reviewed: "x", base: "b",
			diffs: map[[2]string][]string{{"x", "y"}: {"m.go"}, {"b", "y"}: {"pr.go"}}, want: true},
		{name: "path changed by both", reviewed: "x", base: "b",
			diffs: map[[2]string][]string{{"x", "y"}: {"m.go", "both.go"}, {"b", "y"}: {"both.go", "pr.go"}}, want: false, broken: []string{"both.go"}},
		{name: "PR code since review", reviewed: "x", base: "b",
			diffs: map[[2]string][]string{{"x", "y"}: {"pr.go"}, {"b", "y"}: {"pr.go"}}, want: false, broken: []string{"pr.go"}},
		{name: "git error fails closed", reviewed: "x", err: boom, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, broken, err := Covers(context.Background(), fakeGit{tc.diffs, tc.err}, "/r", tc.reviewed, "y", tc.base, 3)
			if (err != nil) != tc.wantErr || ok != tc.want || !reflect.DeepEqual(broken, tc.broken) {
				t.Fatalf("Covers = %v, %v, %v; want %v, %v (err %v)", ok, broken, err, tc.want, tc.broken, tc.wantErr)
			}
		})
	}
}

// TestCoversRealRepo runs Covers against real commits: an update-branch merge
// from main is tolerated, a code push after the review is not, and a path both
// the PR and main changed is not.
func TestCoversRealRepo(t *testing.T) {
	_, dir := gittest.NewRemote(t)
	g := gitx.New("")
	run := func(args ...string) string { return strings.TrimSpace(gittest.Run(t, dir, args...)) }
	put := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", path)
	}
	put("shared.go", "1")
	put("pr.go", "1")
	run("commit", "-q", "-m", "base")
	run("checkout", "-q", "-b", "worker/eng-1-x")
	put("pr.go", "2")
	put(".ard/1/worker-eng.md", "x")
	run("commit", "-q", "-m", "pr")
	reviewed := run("rev-parse", "HEAD")

	// main moves on a file the PR did not touch.
	run("checkout", "-q", "main")
	put("main_only.go", "1")
	run("commit", "-q", "-m", "main change")
	mainTip := run("rev-parse", "HEAD")
	// update-branch: merge main into the PR branch.
	run("checkout", "-q", "worker/eng-1-x")
	run("merge", "-q", "--no-edit", "main")
	merged := run("rev-parse", "HEAD")
	base := run("merge-base", mainTip, merged)
	ctx := context.Background()

	if ok, broken, err := Covers(ctx, g, dir, reviewed, merged, "", 1); err != nil || ok || len(broken) == 0 {
		t.Fatalf("no base: %v %v %v, want not covered", ok, broken, err)
	}
	if ok, broken, err := Covers(ctx, g, dir, reviewed, merged, base, 1); err != nil || !ok {
		t.Fatalf("update-branch merge: %v %v %v, want covered", ok, broken, err)
	}
	// A code push after the review.
	put("pr.go", "3")
	run("commit", "-q", "-m", "more code")
	pushed := run("rev-parse", "HEAD")
	if ok, broken, err := Covers(ctx, g, dir, reviewed, pushed, base, 1); err != nil || ok || !reflect.DeepEqual(broken, []string{"pr.go"}) {
		t.Fatalf("code push: %v %v %v, want pr.go broken", ok, broken, err)
	}
	// A path both the PR and main changed since the review.
	run("checkout", "-q", "main")
	put("pr.go", "9")
	run("commit", "-q", "-m", "main touches pr.go")
	mainTip = run("rev-parse", "HEAD")
	run("checkout", "-q", "-b", "both", reviewed)
	put("pr.go", "10")
	run("commit", "-q", "-m", "pr edits again")
	run("merge", "-q", "--no-edit", "-X", "ours", "main")
	both := run("rev-parse", "HEAD")
	base = run("merge-base", mainTip, both)
	if ok, _, err := Covers(ctx, g, dir, reviewed, both, base, 1); err != nil || ok {
		t.Fatalf("both changed: %v %v, want not covered", ok, err)
	}
}

type evStore []state.Event

func (s evStore) Events(context.Context, state.EventFilter) ([]state.Event, error) { return s, nil }

func TestReviewedHeads(t *testing.T) {
	ev := func(issue int, stage, role, data string) state.Event {
		return state.Event{Kind: state.EventClaim, Issue: issue, Stage: stage, Role: role, Data: []byte(data)}
	}
	s := evStore{
		ev(1, "review", "a", `{"head":"h1"}`),
		ev(1, "review", "a", `{"head":"h2"}`), // newest wins
		ev(1, "review", "b", `{"head":"hb"}`),
		ev(2, "review", "a", `{"head":"other-issue"}`),
		ev(1, "worker", "a", `{"head":"worker"}`),
		ev(1, "review", "c", `{"head":"unwanted"}`),
		ev(1, "review", "a", `not json`),
	}
	got, err := ReviewedHeads(context.Background(), s, 1, []string{"a", "b", "d"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"a": "h2", "b": "hb"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// patchRepo builds a PR branch that edited a 40-line file, reviewed, with main
// editing the other end of the file (or the same line when conflict is set).
type patchRepo struct {
	t   *testing.T
	dir string
	run func(args ...string) string
	put func(path, body string)
}

func newPatchRepo(t *testing.T) *patchRepo {
	_, dir := gittest.NewRemote(t)
	r := &patchRepo{t: t, dir: dir}
	r.run = func(args ...string) string { return strings.TrimSpace(gittest.Run(t, dir, args...)) }
	r.put = func(path, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		r.run("add", path)
	}
	return r
}

func docBody(first, last string) string {
	var sb strings.Builder
	sb.WriteString(first + "\n")
	for i := 0; i < 30; i++ {
		sb.WriteString("line\n")
	}
	sb.WriteString(last + "\n")
	return sb.String()
}

// setup returns the reviewed SHA; the PR edits the last line, main is left on
// the main branch checked out.
func (r *patchRepo) setup() string {
	r.put("doc.md", docBody("a", "z"))
	r.run("commit", "-q", "-m", "base")
	r.run("checkout", "-q", "-b", "worker/eng-1-x")
	r.put("doc.md", docBody("a", "PR"))
	r.run("commit", "-q", "-m", "pr")
	reviewed := r.run("rev-parse", "HEAD")
	r.run("checkout", "-q", "main")
	return reviewed
}

func (r *patchRepo) mainEdit(first string) {
	r.run("checkout", "-q", "main")
	body, _ := os.ReadFile(filepath.Join(r.dir, "doc.md"))
	lines := strings.SplitN(string(body), "\n", 2)
	r.put("doc.md", first+"\n"+lines[1])
	r.run("commit", "-q", "-m", "main edit")
}

func (r *patchRepo) cover(reviewed string) (Cover, error) {
	head := r.run("rev-parse", "HEAD")
	base := r.run("merge-base", "main", head)
	return CoversDetail(context.Background(), gitx.New(""), r.dir, reviewed, head, base, 1)
}

func TestCoversPatchEqualSharedFile(t *testing.T) {
	r := newPatchRepo(t)
	reviewed := r.setup()
	r.mainEdit("main1")
	r.run("checkout", "-q", "worker/eng-1-x")
	r.run("merge", "-q", "--no-edit", "main")
	c, err := r.cover(reviewed)
	if err != nil || !c.OK || !reflect.DeepEqual(c.PatchEqual, []string{"doc.md"}) {
		t.Fatalf("got %+v %v, want covered by patch id", c, err)
	}
	// A second clean update-branch merge.
	r.mainEdit("main2")
	r.run("checkout", "-q", "worker/eng-1-x")
	r.run("merge", "-q", "--no-edit", "main")
	if c, err = r.cover(reviewed); err != nil || !c.OK {
		t.Fatalf("chain of merges: %+v %v", c, err)
	}
	// The PR changes the file again after the clean merge.
	r.put("doc.md", docBody("main2", "PR2"))
	r.run("commit", "-q", "-m", "again")
	if c, err = r.cover(reviewed); err != nil || c.OK || !reflect.DeepEqual(c.Broken, []string{"doc.md"}) {
		t.Fatalf("pr changes again: %+v %v", c, err)
	}
}

func TestCoversConflictResolved(t *testing.T) {
	r := newPatchRepo(t)
	reviewed := r.setup()
	r.run("checkout", "-q", "main")
	r.put("doc.md", docBody("a", "MAIN"))
	r.run("commit", "-q", "-m", "main edit")
	r.run("checkout", "-q", "worker/eng-1-x")
	r.run("merge", "-q", "--no-commit", "--no-ff", "-s", "ours", "main") // stands in for a conflicting merge
	r.put("doc.md", docBody("a", "RESOLVED"))
	r.run("commit", "-q", "-m", "merge resolved")
	c, err := r.cover(reviewed)
	if err != nil || c.OK || !reflect.DeepEqual(c.Broken, []string{"doc.md"}) {
		t.Fatalf("conflict resolved: %+v %v", c, err)
	}
}

func TestCoversHandEditInMerge(t *testing.T) {
	r := newPatchRepo(t)
	reviewed := r.setup()
	r.mainEdit("main1")
	r.run("checkout", "-q", "worker/eng-1-x")
	r.run("merge", "-q", "--no-commit", "--no-ff", "main")
	r.put("doc.md", docBody("main1", "HAND"))
	r.run("commit", "-q", "-m", "merge with edit")
	c, err := r.cover(reviewed)
	if err != nil || c.OK || !reflect.DeepEqual(c.Broken, []string{"doc.md"}) {
		t.Fatalf("hand edit: %+v %v", c, err)
	}
}

type errPatchGit struct {
	Git
	mbErr, parErr, patchErr error
}

func (g errPatchGit) MergeBase(context.Context, string, string, string) (string, error) {
	return "ob", g.mbErr
}
func (g errPatchGit) Parents(context.Context, string, string) ([]string, error) {
	return []string{"rev", "main"}, g.parErr
}
func (g errPatchGit) PatchID(context.Context, string, string, string, string) (string, error) {
	return "id", g.patchErr
}

func TestCoversPatchGitError(t *testing.T) {
	boom := errors.New("boom")
	fg := fakeGit{diffs: map[[2]string][]string{
		{"rev", "head"}:  {"doc.md"},
		{"base", "head"}: {"doc.md"},
	}}
	for name, g := range map[string]errPatchGit{
		"ok":      {Git: fg},
		"mb":      {Git: fg, mbErr: boom},
		"parents": {Git: fg, parErr: boom},
		"patch":   {Git: fg, patchErr: boom},
	} {
		c, err := CoversDetail(context.Background(), g, "", "rev", "head", "base", 1)
		if name == "ok" {
			if err != nil || !c.OK {
				t.Errorf("%s: %+v %v", name, c, err)
			}
			continue
		}
		if err != boom || c.OK {
			t.Errorf("%s: %+v %v, want boom and not covered", name, c, err)
		}
	}
	// base "" never uses the rule.
	c, err := CoversDetail(context.Background(), errPatchGit{Git: fg, patchErr: boom}, "", "rev", "head", "", 1)
	if err != nil || c.OK || len(c.PatchEqual) != 0 {
		t.Errorf("base empty: %+v %v", c, err)
	}
}
