package gitx

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

func commitAll(t *testing.T, dir, msg string) string {
	t.Helper()
	gittest.Run(t, dir, "add", "-A")
	gittest.Run(t, dir, "commit", "-q", "-m", msg)
	return strings.TrimSpace(gittest.Run(t, dir, "rev-parse", "HEAD"))
}

// reviewFixture returns a clone, the base commit and a bare repo holding both.
func reviewFixture(t *testing.T) (clone, base string) {
	_, clone = gittest.NewRemote(t)
	write(t, clone, "mod.txt", "1\n2\n3\n4\n5\n")
	write(t, clone, "old.txt", "a\nb\nc\nd\ne\nf\ng\nh\n")
	write(t, clone, "del.txt", "x\ny\n")
	write(t, clone, ".ard/9/plan.md", "plan\n")
	return clone, commitAll(t, clone, "base")
}

func TestDiffStatExcludeAndAttributes(t *testing.T) {
	ctx := context.Background()
	g := New("")
	clone, base := reviewFixture(t)
	write(t, clone, ".gitattributes", "* -diff\n*.go binary\n")
	write(t, clone, "hello.go", "package x\n\nfunc F() {}\n")
	write(t, clone, ".ard/9/review.md", "secret-ish\n")
	tip := commitAll(t, clone, "change")
	patch, trunc, err := g.Diff(ctx, clone, base, tip, 1<<20, ".ard/")
	if err != nil || trunc {
		t.Fatal(err, trunc)
	}
	s := string(patch)
	for _, want := range []string{"hello.go       |", "+func F() {}", "+package x"} {
		if !strings.Contains(s, want) {
			t.Errorf("patch lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, ".ard/") {
		t.Errorf(".ard/ was not left out:\n%s", s)
	}
	if _, _, err := g.Diff(ctx, clone, base, tip, 1<<20, "../x/"); err == nil {
		t.Error("a bad exclude prefix must be refused")
	}
	if _, _, err := g.Diff(ctx, clone, base, tip, 1<<20, "a b/"); err == nil {
		t.Error("a bad exclude prefix must be refused")
	}
}

func TestDiffTruncatesAtLineEnd(t *testing.T) {
	ctx := context.Background()
	g := New("")
	clone, base := reviewFixture(t)
	write(t, clone, "big.txt", strings.Repeat("0123456789\n", 200))
	tip := commitAll(t, clone, "big")
	patch, trunc, err := g.Diff(ctx, clone, base, tip, 500)
	if err != nil || !trunc {
		t.Fatal(err, trunc)
	}
	if len(patch) > 500 || !strings.HasSuffix(string(patch), "\n") {
		t.Errorf("len %d, ends %q", len(patch), string(patch[len(patch)-5:]))
	}
}

func TestAddedLinesBetween(t *testing.T) {
	ctx := context.Background()
	g := New("")
	clone, base := reviewFixture(t)
	write(t, clone, "mod.txt", "1\n2\nTHREE\n4\n5\n6\n7\n") // line 3 edited, 6-7 appended
	write(t, clone, "del.txt", "x\n")                       // a pure deletion
	gittest.Run(t, clone, "mv", "old.txt", "new.txt")       //
	write(t, clone, "new.txt", "a\nb\nc\nd\nE\nf\ng\nh\n")  // rename with one edited line
	write(t, clone, ".ard/9/review.md", "r\n")              // excluded
	tip := commitAll(t, clone, "change")
	got, err := g.AddedLinesBetween(ctx, clone, base, tip, ".ard/")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]LineRange{
		"mod.txt": {{3, 3}, {6, 7}},
		"new.txt": {{5, 5}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// Only an excluded path changed: nothing.
	write(t, clone, ".ard/9/more.md", "m\n")
	tip2 := commitAll(t, clone, "ard only")
	none, err := g.AddedLinesBetween(ctx, clone, tip, tip2, ".ard/")
	if err != nil || len(none) != 0 {
		t.Errorf("none = %v, %v", none, err)
	}
	ch, err := g.CodeChanged(ctx, clone, tip, tip2, ".ard/")
	if err != nil || len(ch) != 0 {
		t.Errorf("CodeChanged = %v, %v", ch, err)
	}
	ch, err = g.CodeChanged(ctx, clone, base, tip, ".ard/")
	if err != nil || len(ch) == 0 {
		t.Errorf("CodeChanged = %v, %v", ch, err)
	}
}

func TestReviewDiffInBareRepo(t *testing.T) {
	ctx := context.Background()
	g := New("")
	clone, base := reviewFixture(t)
	write(t, clone, "mod.txt", "1\n2\nX\n4\n5\n")
	tip := commitAll(t, clone, "change")
	bare := t.TempDir()
	gittest.Run(t, bare, "init", "-q", "--bare", ".")
	gittest.Run(t, clone, "push", "-q", bare, "HEAD:refs/heads/x")
	patch, _, err := g.Diff(ctx, bare, base, tip, 1<<20, ".ard/")
	if err != nil || !strings.Contains(string(patch), "+X") {
		t.Fatalf("bare diff: %v\n%s", err, patch)
	}
	al, err := g.AddedLinesBetween(ctx, bare, base, tip, ".ard/")
	if err != nil || !reflect.DeepEqual(al, map[string][]LineRange{"mod.txt": {{3, 3}}}) {
		t.Errorf("bare added = %v, %v", al, err)
	}
}
