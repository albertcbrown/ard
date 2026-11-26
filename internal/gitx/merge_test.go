package gitx_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

func commitFile(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, dir, "add", name)
	gittest.Run(t, dir, "commit", "-m", msg)
}

// mergeFixture returns a clone on branch "side" and the tip of "other", which
// edits the same file.
func mergeFixture(t *testing.T, sideContent, otherContent string) (clone, other string) {
	t.Helper()
	_, clone = gittest.NewRemote(t)
	commitFile(t, clone, "a.txt", "one\n", "base")
	gittest.Run(t, clone, "checkout", "-q", "-b", "other")
	commitFile(t, clone, "a.txt", otherContent, "other")
	other = strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	gittest.Run(t, clone, "checkout", "-q", "-b", "side", "main")
	gittest.Run(t, clone, "reset", "-q", "--hard", "other~1")
	commitFile(t, clone, "a.txt", sideContent, "side")
	return clone, other
}

func TestMergeNoCommitConflict(t *testing.T) {
	clone, other := mergeFixture(t, "side\n", "other\n")
	got, err := gitx.New("").MergeNoCommit(context.Background(), clone, other)
	if err != nil || len(got) != 1 || got[0] != "a.txt" {
		t.Fatalf("MergeNoCommit = %v, %v; want [a.txt]", got, err)
	}
	if err := gitx.New("").MergeAbort(context.Background(), clone); err != nil {
		t.Fatal(err)
	}
}

func TestMergeNoCommitClean(t *testing.T) {
	_, clone := gittest.NewRemote(t)
	commitFile(t, clone, "a.txt", "one\n", "base")
	gittest.Run(t, clone, "checkout", "-q", "-b", "other")
	commitFile(t, clone, "b.txt", "b\n", "other")
	other := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	gittest.Run(t, clone, "checkout", "-q", "main")
	got, err := gitx.New("").MergeNoCommit(context.Background(), clone, other)
	if err != nil || len(got) != 0 {
		t.Fatalf("MergeNoCommit = %v, %v; want clean", got, err)
	}
}

func TestMergeNoCommitOtherError(t *testing.T) {
	_, clone := gittest.NewRemote(t)
	if got, err := gitx.New("").MergeNoCommit(context.Background(), clone, "no-such-ref"); err == nil {
		t.Fatalf("MergeNoCommit(bad ref) = %v, nil; want error", got)
	}
}

// A branch's .gitattributes must not change how the merge resolves.
func TestMergeIgnoresAttributes(t *testing.T) {
	clone, other := mergeFixture(t, "side\n", "other\n")
	commitFile(t, clone, ".gitattributes", "a.txt merge=union\n", "attrs")
	got, err := gitx.New("").MergeNoCommit(context.Background(), clone, other)
	if err != nil || len(got) != 1 {
		t.Fatalf("MergeNoCommit = %v, %v; want a conflict despite merge=union", got, err)
	}
}

func TestCommitParents(t *testing.T) {
	_, clone := gittest.NewRemote(t)
	commitFile(t, clone, "a.txt", "one\n", "base")
	tip := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	gittest.Run(t, clone, "checkout", "-q", "-b", "other", "HEAD~1")
	commitFile(t, clone, "b.txt", "b\n", "other")
	other := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	g := gitx.New("")
	op := gitx.CommitOp{Dir: clone, Message: "merge\n", Author: gitx.Identity{Name: "n", Email: "n@example.com"}, Parents: []string{tip, other}}
	// The index equals HEAD's tree here only if we stage nothing new: a
	// merge commit is still written.
	sha, err := g.Commit(context.Background(), gitx.CommitOp{Dir: clone, Message: op.Message, Author: op.Author, Parents: op.Parents})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := g.Parents(context.Background(), clone, sha)
	if err != nil || len(ps) != 2 || ps[0] != tip || ps[1] != other {
		t.Fatalf("parents = %v, %v; want [%s %s]", ps, err, tip, other)
	}
	if head := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD")); head != sha {
		t.Fatalf("HEAD = %s, want %s", head, sha)
	}
}
