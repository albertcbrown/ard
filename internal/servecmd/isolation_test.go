//go:build unix

package servecmd

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/repopath"
	"github.com/albertcbrown/ard/internal/state"
)

func isoRepo(t *testing.T, s string) github.Repo {
	t.Helper()
	o, n, ok := strings.Cut(s, "/")
	if !ok {
		t.Fatalf("bad repo %q", s)
	}
	return github.Repo{Owner: o, Name: n}
}

// repoPaths returns every per-repository root ard uses, keyed by kind.
func repoPaths(home string, repo github.Repo) (map[string]string, error) {
	out := map[string]string{}
	var err error
	set := func(k, p string, e error) {
		if e != nil && err == nil {
			err = e
		}
		out[k] = p
	}
	root := filepath.Join(home, ".ard")
	p, e := state.Dir(root, repo.String(), false)
	set("state", p, e)
	p, e = state.Dir(root, repo.String(), true)
	set("state-dryrun", p, e)
	owner, name, e := repopath.Parts(repo.String())
	if e != nil {
		return nil, e
	}
	p, e = controller.LockPath(home, owner, name)
	set("lock", p, e)
	p, e = clonePath(home, repo)
	set("clone", p, e)
	p, e = repoWorkDir(home, repo)
	set("work", p, e)
	for _, kind := range []string{"logs", "config", "roles-repo"} {
		p, e = repopath.Join(filepath.Join(root, kind), repo.String())
		set(kind, p, e)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func isPrefix(a, b string) bool {
	return strings.HasPrefix(b, a+string(filepath.Separator))
}

func TestRepoPathsDistinct(t *testing.T) {
	home := t.TempDir()
	pairs := [][2]string{
		{"a-b/c", "a/b-c"}, {"a/b", "a/b.git"}, {"a/b", "a/b.lock"},
		{"a/b.git", "a/b.git.git"}, {"a.b/c", "a/b.c"}, {"a_b/c", "a/b_c"},
		{"a_b/c", "a-b/c"}, {"a/b_c", "a/b-c"}, {"o/.github", "o/github"},
	}
	for _, pr := range pairs {
		x, err := repoPaths(home, isoRepo(t, pr[0]))
		if err != nil {
			t.Fatalf("%s: %v", pr[0], err)
		}
		y, err := repoPaths(home, isoRepo(t, pr[1]))
		if err != nil {
			t.Fatalf("%s: %v", pr[1], err)
		}
		for kx, px := range x {
			for ky, py := range y {
				if px == py || isPrefix(px, py) || isPrefix(py, px) {
					t.Errorf("%s %s (%s) collides with %s %s (%s)", pr[0], kx, px, pr[1], ky, py)
				}
			}
		}
	}
}

func TestRepoPathsCaseFold(t *testing.T) {
	home := t.TempDir()
	for _, set := range [][]string{
		{"Owner/Repo", "owner/repo", "OWNER/REPO"},
		{"A/B.git", "a/b.git"},
	} {
		want, err := repoPaths(home, isoRepo(t, set[0]))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range set[1:] {
			got, err := repoPaths(home, isoRepo(t, s))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s: paths differ from %s:\n%v\n%v", s, set[0], got, want)
			}
		}
	}
}

func TestRepoPathsInvalid(t *testing.T) {
	for _, s := range []string{"a/b/c", "../x", "a/.."} {
		if m, err := repoPaths(t.TempDir(), isoRepo(t, s)); err == nil {
			t.Errorf("%q: want error, got %v", s, m)
		}
	}
}

func TestRepoPathsDryRunSplit(t *testing.T) {
	m, err := repoPaths(t.TempDir(), isoRepo(t, "a/b"))
	if err != nil {
		t.Fatal(err)
	}
	if m["state"] == m["state-dryrun"] {
		t.Error("live and dry-run state share a directory")
	}
}

func TestServeLocksPerRepo(t *testing.T) {
	home := t.TempDir()
	acquire := func(repo string) (*controller.Leader, error) {
		o, n, _ := strings.Cut(repo, "/")
		p, err := controller.LockPath(home, o, n)
		if err != nil {
			t.Fatal(err)
		}
		return controller.AcquireLeader(p)
	}
	ab, err := acquire("a/b")
	if err != nil {
		t.Fatal(err)
	}
	cd, err := acquire("c/d")
	if err != nil {
		t.Fatalf("different repo should lock: %v", err)
	}
	defer cd.Release()
	for _, r := range []string{"a/b", "A/B"} {
		if _, err := acquire(r); !errors.Is(err, controller.ErrLeaderHeld) {
			t.Errorf("%s: want ErrLeaderHeld, got %v", r, err)
		}
	}
	if err := ab.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := acquire("A/B")
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	again.Release()
}
