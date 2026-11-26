package committer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

// mergeEnv has a PR branch with new.go cut from the seed, and main moved on
// by one commit (b.txt and a file under .ard/).
type mergeEnv struct {
	*codeEnv
	branchTip string
	mainTip   string
}

func newMergeEnv(t *testing.T) *mergeEnv {
	t.Helper()
	c := newCodeEnv(t, nil)
	r := c.req(CodeFile{Path: "new.go", Src: c.srcFile(t, "new.go", "package x\n")})
	r.Create = true
	if _, err := c.b.CommitCode(context.Background(), brName, r); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"b.txt": "b\n", ".ard/5/plan.md": "plan\n"} {
		p := filepath.Join(c.clone, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Run(t, c.clone, "add", "-A")
	gittest.Run(t, c.clone, "commit", "-q", "-m", "main moves")
	gittest.Run(t, c.clone, "push", "-q", "origin", "main")
	return &mergeEnv{codeEnv: c, branchTip: c.head(t, brName), mainTip: c.head(t, "main")}
}

func (m *mergeEnv) req(files ...CodeFile) CodeRequest {
	r := m.codeEnv.req(files...)
	r.Base, r.MergeParent = m.branchTip, m.mainTip
	return r
}

func (m *mergeEnv) own(t *testing.T) CodeFile {
	return CodeFile{Path: "new.go", Src: m.srcFile(t, "new.go", "package x\n")}
}

func TestCommitCodeMergeParent(t *testing.T) {
	m := newMergeEnv(t)
	res, err := m.b.CommitCode(context.Background(), brName, m.req(m.own(t)))
	if err != nil {
		t.Fatal(err)
	}
	if head := m.head(t, brName); res.SHA != head {
		t.Fatalf("res %s head %s", res.SHA, head)
	}
	ps := strings.Fields(gittest.Run(t, m.bare, "rev-list", "--parents", "-n1", brName))
	if len(ps) != 3 || ps[1] != m.branchTip || ps[2] != m.mainTip {
		t.Fatalf("parents %v, want [%s %s]", ps[1:], m.branchTip, m.mainTip)
	}
	if want := gittest.Run(t, m.bare, "ls-tree", "-r", m.mainTip); !strings.Contains(m.tree(t, brName), strings.Split(want, "\n")[0]) {
		t.Errorf("tree lacks main's files")
	}
	tree := m.tree(t, brName)
	for _, f := range []string{"\tnew.go", "\tb.txt", "\t.ard/5/plan.md"} {
		if !strings.Contains(tree, f) {
			t.Errorf("tree lacks %s:\n%s", f, tree)
		}
	}
}

func TestCommitCodeMergeParentNotMainTip(t *testing.T) {
	m := newMergeEnv(t)
	r := m.req(m.own(t))
	r.MergeParent = m.base // an older main commit
	if _, err := m.b.CommitCode(context.Background(), brName, r); !errors.Is(err, ErrMergeParent) {
		t.Fatalf("err = %v, want ErrMergeParent", err)
	}
	if m.head(t, brName) != m.branchTip {
		t.Fatal("branch moved")
	}
}

func TestCommitCodeMergeParentInvalid(t *testing.T) {
	m := newMergeEnv(t)
	r := m.req(m.own(t))
	r.MergeParent = "main"
	if _, err := m.b.CommitCode(context.Background(), brName, r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-hex: %v", err)
	}
	r = m.req(m.own(t))
	r.Create = true
	if _, err := m.b.CommitCode(context.Background(), brName, r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("with Create: %v", err)
	}
}

// A path that differs from main and is not declared cannot appear: the result
// is main plus the declared files, and a declared path that main has changed
// is simply overwritten by the declared content. An undeclared difference is
// refused by the staged check, so the worker cannot revert main's change by
// omission or smuggle a path in.
func TestCommitCodeMergeProtectedRefused(t *testing.T) {
	m := newMergeEnv(t)
	for _, p := range []string{".github/workflows/x.yml", ".ard/5/plan.md", ".gitattributes"} {
		r := m.req(m.own(t), CodeFile{Path: p, Src: m.srcFile(t, "evil", "x\n")})
		if _, err := m.b.CommitCode(context.Background(), brName, r); err == nil {
			t.Fatalf("%s: accepted", p)
		}
		if m.head(t, brName) != m.branchTip {
			t.Fatalf("%s: branch moved", p)
		}
	}
	// Deleting a protected path main has is refused too.
	r := m.req(m.own(t), CodeFile{Path: ".ard/5/plan.md", Delete: true})
	if _, err := m.b.CommitCode(context.Background(), brName, r); err == nil {
		t.Fatal("protected delete accepted")
	}
}

func TestCommitCodeMergeBranchMovedNoRetry(t *testing.T) {
	m := newMergeEnv(t)
	r := m.req(m.own(t))
	r.Base = m.base // the worker's view of the branch is stale
	if _, err := m.b.CommitCode(context.Background(), brName, r); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if m.head(t, brName) != m.branchTip {
		t.Fatal("branch moved")
	}
}

func TestCommitCodeMergeSameTreeAsMain(t *testing.T) {
	m := newMergeEnv(t)
	// Declare new.go as deleted: the tree equals main's, still a commit.
	res, err := m.b.CommitCode(context.Background(), brName, m.req(CodeFile{Path: "new.go", Delete: true}))
	if err != nil {
		t.Fatal(err)
	}
	if res.SHA != m.head(t, brName) || res.SHA == m.branchTip {
		t.Fatalf("merge not pushed: %+v", res)
	}
}

func TestCommitCodeMergeOverMaxFiles(t *testing.T) {
	m := newMergeEnv(t)
	var fs []CodeFile
	for i := 0; i <= MaxFiles; i++ {
		fs = append(fs, CodeFile{Path: "f" + strings.Repeat("x", i%7) + string(rune('a'+i/26)) + string(rune('a'+i%26)) + ".txt", Delete: true})
	}
	if _, err := m.b.CommitCode(context.Background(), brName, m.req(fs...)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestCommitCodeMergeMainMovedAfterFetch(t *testing.T) {
	m := newMergeEnv(t)
	if err := os.WriteFile(filepath.Join(m.clone, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, m.clone, "add", "-A")
	gittest.Run(t, m.clone, "commit", "-q", "-m", "main moves again")
	gittest.Run(t, m.clone, "push", "-q", "origin", "main")
	if _, err := m.b.CommitCode(context.Background(), brName, m.req(m.own(t))); !errors.Is(err, ErrMergeParent) {
		t.Fatalf("err = %v, want ErrMergeParent", err)
	}
}
