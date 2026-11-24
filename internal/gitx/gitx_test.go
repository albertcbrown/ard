package gitx

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

var author = Identity{Name: "ard[bot]", Email: "1+ard[bot]@users.noreply.github.com"}

func stage(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, dir, "add", name)
}

func TestCommit(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, clone := gittest.NewRemote(t)

	t.Run("empty index", func(t *testing.T) {
		if _, err := g.Commit(ctx, CommitOp{Dir: clone, Message: "x", Author: author}); !errors.Is(err, ErrNothingToCommit) {
			t.Fatalf("err = %v, want ErrNothingToCommit", err)
		}
	})
	t.Run("missing author", func(t *testing.T) {
		if _, err := g.Commit(ctx, CommitOp{Dir: clone, Message: "x"}); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("commits staged", func(t *testing.T) {
		stage(t, clone, "a.txt", "a")
		paths, err := g.StagedPaths(ctx, clone)
		if err != nil || len(paths) != 1 || paths[0] != "a.txt" {
			t.Fatalf("StagedPaths = %v, %v", paths, err)
		}
		sha, err := g.Commit(ctx, CommitOp{Dir: clone, Message: "add a\n\ntrailer: yes", Author: author})
		if err != nil {
			t.Fatal(err)
		}
		head, _ := g.RevParse(ctx, clone, "HEAD")
		if sha != head || len(sha) != 40 {
			t.Fatalf("sha %q head %q", sha, head)
		}
		got := gittest.Run(t, clone, "log", "-1", "--format=%an <%ae>|%cn <%ce>|%B")
		want := "ard[bot] <1+ard[bot]@users.noreply.github.com>|ard[bot] <1+ard[bot]@users.noreply.github.com>|add a"
		if !strings.HasPrefix(got, want) || !strings.Contains(got, "trailer: yes") {
			t.Fatalf("log = %q", got)
		}
		if n := strings.TrimSpace(gittest.Run(t, clone, "rev-list", "--count", "HEAD")); n != "2" {
			t.Fatalf("commit count %s", n)
		}
	})
}

func TestPush(t *testing.T) {
	ctx := context.Background()
	g := New("")

	t.Run("moves ref", func(t *testing.T) {
		bare, clone := gittest.NewRemote(t)
		stage(t, clone, "a.txt", "a")
		sha, err := g.Commit(ctx, CommitOp{Dir: clone, Message: "a", Author: author})
		if err != nil {
			t.Fatal(err)
		}
		if err := g.Push(ctx, PushOp{Dir: clone, Remote: "origin", Ref: "HEAD:refs/heads/main"}); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(gittest.Run(t, bare, "rev-parse", "main")); got != sha {
			t.Fatalf("remote main = %s, want %s", got, sha)
		}
	})

	t.Run("non fast forward", func(t *testing.T) {
		bare, clone := gittest.NewRemote(t)
		other := filepath.Join(t.TempDir(), "other")
		gittest.Run(t, filepath.Dir(other), "clone", bare, other)
		stage(t, other, "o.txt", "o")
		if _, err := g.Commit(ctx, CommitOp{Dir: other, Message: "o", Author: author}); err != nil {
			t.Fatal(err)
		}
		if err := g.Push(ctx, PushOp{Dir: other, Remote: "origin", Ref: "HEAD:refs/heads/main"}); err != nil {
			t.Fatal(err)
		}
		stage(t, clone, "c.txt", "c")
		if _, err := g.Commit(ctx, CommitOp{Dir: clone, Message: "c", Author: author}); err != nil {
			t.Fatal(err)
		}
		err := g.Push(ctx, PushOp{Dir: clone, Remote: "origin", Ref: "HEAD:refs/heads/main"})
		if !errors.Is(err, ErrNonFastForward) {
			t.Fatalf("err = %v, want ErrNonFastForward", err)
		}
	})

	t.Run("lease", func(t *testing.T) {
		bare, clone := gittest.NewRemote(t)
		base := strings.TrimSpace(gittest.Run(t, bare, "rev-parse", "main"))
		stage(t, clone, "a.txt", "a")
		if _, err := g.Commit(ctx, CommitOp{Dir: clone, Message: "a", Author: author}); err != nil {
			t.Fatal(err)
		}
		stale := strings.Repeat("1", 40)
		err := g.Push(ctx, PushOp{Dir: clone, Remote: "origin", Ref: "HEAD:refs/heads/main", ForceWithLease: stale})
		if !errors.Is(err, ErrStaleLease) {
			t.Fatalf("err = %v, want ErrStaleLease", err)
		}
		if err := g.Push(ctx, PushOp{Dir: clone, Remote: "origin", Ref: "HEAD:refs/heads/main", ForceWithLease: base}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("token called once", func(t *testing.T) {
		_, clone := gittest.NewRemote(t)
		n := 0
		tok := func(context.Context) (string, error) { n++; return "secret-token", nil }
		if err := g.Push(ctx, PushOp{Dir: clone, Remote: "origin", Ref: "HEAD:refs/heads/x", Token: tok}); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("token calls = %d", n)
		}
	})

	t.Run("token error", func(t *testing.T) {
		_, clone := gittest.NewRemote(t)
		boom := errors.New("boom")
		err := g.Push(ctx, PushOp{Dir: clone, Remote: "origin", Ref: "HEAD:refs/heads/x",
			Token: func(context.Context) (string, error) { return "", boom }})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestPushEnv(t *testing.T) {
	env := pushEnv("tok", "https://github.com/o/r.git")
	b64 := base64.StdEncoding.EncodeToString([]byte("x-access-token:tok"))
	want := []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader", "GIT_CONFIG_VALUE_0=Authorization: Basic " + b64}
	if strings.Join(env, "\n") != strings.Join(want, "\n") {
		t.Fatalf("env = %v", env)
	}
}

func TestScrub(t *testing.T) {
	tok := "ghs_abc123"
	b1 := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + tok))
	b2 := base64.StdEncoding.EncodeToString([]byte(tok))
	for _, in := range []string{"fatal: " + tok, "hdr " + b1, "hdr " + b2} {
		got := scrub(in, tok)
		if strings.Contains(got, tok) || strings.Contains(got, b1) || strings.Contains(got, b2) {
			t.Errorf("scrub(%q) = %q", in, got)
		}
	}
	if got := scrub("plain", ""); got != "plain" {
		t.Errorf("got %q", got)
	}
}

func TestRevParseError(t *testing.T) {
	_, clone := gittest.NewRemote(t)
	if _, err := New("").RevParse(context.Background(), clone, "nope"); err == nil {
		t.Fatal("want error")
	}
}

func TestChangedSince(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, clone := gittest.NewRemote(t)
	mk := func(rel, body string) {
		p := filepath.Join(clone, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk(".ard/5/issue.md", "a")
	mk(".ard/5/gone.md", "a")
	mk(".ard/6/other.md", "a")
	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "-m", "base")
	base := gittest.Run(t, clone, "rev-parse", "HEAD")
	base = strings.TrimSpace(base)

	mk(".ard/5/issue.md", "changed")
	mk(".ard/5/new.md", "n")
	mk(".ard/5/sub dir/x y.md", "n")
	mk(".ard/6/other.md", "changed")
	if err := os.Remove(filepath.Join(clone, ".ard/5/gone.md")); err != nil {
		t.Fatal(err)
	}
	got, err := g.ChangedSince(ctx, clone, clone, base, ".ard/5/")
	if err != nil {
		t.Fatal(err)
	}
	want := []PathChange{{Path: ".ard/5/gone.md", Deleted: true}, {Path: ".ard/5/issue.md", Mode: "100644"}, {Path: ".ard/5/new.md", Mode: "100644"}, {Path: ".ard/5/sub dir/x y.md", Mode: "100644"}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if _, err := g.ChangedSince(ctx, clone, clone, "deadbeef", ".ard/5/"); err == nil {
		t.Fatal("bad base: want error")
	}
}

func TestRemoteURL(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, clone := gittest.NewRemote(t)
	gittest.Run(t, clone, "remote", "set-url", "origin", "git@github.com:o/r.git")

	got, err := g.RemoteURL(ctx, clone, "origin")
	if err != nil || got != "git@github.com:o/r.git" {
		t.Errorf("RemoteURL = %q, %v", got, err)
	}
	if _, err := g.RemoteURL(ctx, clone, "nope"); err == nil {
		t.Error("missing remote: want error")
	}
}

func TestHooksNeverRun(t *testing.T) {
	ctx := context.Background()
	g := New("")
	bare, clone := gittest.NewRemote(t)
	marker := filepath.Join(t.TempDir(), "ran")
	for _, h := range []string{"reference-transaction", "pre-commit", "pre-push", "post-checkout", "post-commit"} {
		script := "#!/bin/sh\ntouch " + marker + "\n"
		if err := os.WriteFile(filepath.Join(clone, ".git", "hooks", h), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stage(t, clone, "h.txt", "h")
	// GIT_DIR in the parent must not redirect anything either.
	t.Setenv("GIT_DIR", filepath.Join(bare))
	if err := g.FetchRef(ctx, clone, "file://"+bare, "+refs/heads/main:refs/ard/up/main", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(ctx, CommitOp{Dir: clone, Message: "m", Author: author}); err != nil {
		t.Fatal(err)
	}
	if err := g.Push(ctx, PushOp{Dir: clone, Remote: "file://" + bare, Ref: "HEAD:refs/heads/hooked"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a hook ran")
	}
}

func TestHeaderKey(t *testing.T) {
	tests := map[string]string{
		"https://github.com/o/r.git": "http.https://github.com/.extraheader",
		"file:///tmp/x.git":          "http.extraheader",
		"/tmp/x.git":                 "http.extraheader",
	}
	for in, want := range tests {
		if got := headerKey(in); got != want {
			t.Errorf("headerKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func worktreeCount(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	for _, l := range strings.Split(gittest.Run(t, dir, "worktree", "list", "--porcelain"), "\n") {
		if strings.HasPrefix(l, "worktree ") {
			n++
		}
	}
	return n
}

func TestWorktreeAdminConcurrent(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, clone := gittest.NewRemote(t)
	work := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wt := filepath.Join(work, fmt.Sprintf("wt-%d", i))
			for j := 0; j < 25; j++ {
				if err := g.WorktreeAdd(ctx, clone, wt, "HEAD"); err != nil {
					t.Errorf("lane %d add %d: %v", i, j, err)
					return
				}
				if err := g.WorktreeRemove(ctx, clone, wt); err != nil {
					t.Errorf("lane %d remove %d: %v", i, j, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if n := worktreeCount(t, clone); n != 1 {
		t.Errorf("%d worktrees left, want 1", n)
	}
}

func TestWorktreeLockKey(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, clone := gittest.NewRemote(t)
	_, other := gittest.NewRemote(t)
	linked := filepath.Join(t.TempDir(), "linked")
	gittest.Run(t, clone, "worktree", "add", "--detach", linked, "HEAD")

	unlock, err := g.worktreeLock(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := g.worktreeLock(short, linked); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("linked worktree err = %v, want DeadlineExceeded", err)
	}
	u2, err := g.worktreeLock(ctx, other)
	if err != nil {
		t.Fatalf("unrelated clone blocked: %v", err)
	}
	u2()
}

func TestWorktreeRemoveRunsWhenCancelled(t *testing.T) {
	g := New("")
	_, clone := gittest.NewRemote(t)
	wt := filepath.Join(t.TempDir(), "wt")
	if err := g.WorktreeAdd(context.Background(), clone, wt, "HEAD"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.WorktreeRemove(ctx, clone, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree still there: %v", err)
	}
	if n := worktreeCount(t, clone); n != 1 {
		t.Errorf("%d worktrees, want 1", n)
	}
}

func TestSetExec(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, clone := gittest.NewRemote(t)
	gittest.Run(t, clone, "config", "core.fileMode", "false")
	stage(t, clone, "tool", "#!/bin/sh\n")
	stage(t, clone, "plain", "x\n")
	if err := g.SetExec(ctx, clone, "tool"); err != nil {
		t.Fatal(err)
	}
	changes, err := g.StagedChanges(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range changes {
		got[c.Path] = c.NewMode
	}
	if got["tool"] != "100755" || got["plain"] != "100644" {
		t.Errorf("modes = %v, want tool 100755 and plain 100644", got)
	}
	if err := g.SetExec(ctx, clone, "missing"); err == nil {
		t.Error("SetExec on an unstaged path succeeded")
	}
}

func TestIsBareRepo(t *testing.T) {
	ctx := context.Background()
	g := New("")
	bare, clone := gittest.NewRemote(t)
	empty := filepath.Join(clone, "emptydir")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		dir     string
		want    bool
		wantErr bool
	}{
		{"bare repo", bare, true, false},
		{"git dir of a non-bare repo", filepath.Join(clone, ".git"), false, false},
		{"top of a non-bare repo", clone, false, true},
		{"empty dir inside a repo does not walk up", empty, false, true},
		{"missing path", filepath.Join(t.TempDir(), "nope"), false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := g.IsBareRepo(ctx, tc.dir)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("IsBareRepo = %v, %v; want %v (err %v)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestSetConfig(t *testing.T) {
	ctx := context.Background()
	g := New("")
	bare, _ := gittest.NewRemote(t)
	for i := 0; i < 2; i++ { // idempotent
		if err := g.SetConfig(ctx, bare, "gc.auto", "0"); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.TrimSpace(gittest.Run(t, bare, "config", "--get", "gc.auto")); got != "0" {
		t.Fatalf("gc.auto = %q", got)
	}
	if err := g.SetConfig(ctx, filepath.Join(t.TempDir(), "nope"), "gc.auto", "0"); err == nil {
		t.Fatal("missing dir accepted")
	}
}

func TestRunDisablesAutoGC(t *testing.T) {
	g := New("")
	_, clone := gittest.NewRemote(t)
	for _, c := range [][2]string{{"gc.auto", "0"}, {"maintenance.auto", "false"}} {
		out, _, err := g.run(context.Background(), clone, nil, "", nil, "config", "--get", c[0])
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(out); got != c[1] {
			t.Errorf("%s = %q, want %q", c[0], got, c[1])
		}
	}
}

// TestWorktreeAdminWithAutoGC runs add/remove cycles next to fetches in a repo
// configured to auto-gc after every fetch. A detached gc --auto would prune
// .git/worktrees outside the lock; run's -c gc.auto=0 must prevent that.
func TestWorktreeAdminWithAutoGC(t *testing.T) {
	ctx := context.Background()
	g := New("")
	bare, clone := gittest.NewRemote(t)
	gittest.Run(t, clone, "config", "gc.auto", "1")
	gittest.Run(t, clone, "config", "gc.autoDetach", "true")
	gittest.Run(t, clone, "config", "maintenance.auto", "true")
	for i := 0; i < 60; i++ { // loose objects, so gc --auto would trigger
		if err := os.WriteFile(filepath.Join(clone, fmt.Sprintf("o%d", i)), []byte(fmt.Sprint(i)), 0o644); err != nil {
			t.Fatal(err)
		}
		gittest.Run(t, clone, "hash-object", "-w", fmt.Sprintf("o%d", i))
	}
	work := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if err := g.FetchRef(ctx, clone, "file://"+bare, "+refs/heads/main:refs/ard/up/main", nil); err != nil {
					t.Errorf("fetch %d: %v", i, err)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			wt := filepath.Join(work, fmt.Sprintf("wt-%d", i))
			for j := 0; j < 15; j++ {
				if err := g.WorktreeAdd(ctx, clone, wt, "HEAD"); err != nil {
					t.Errorf("lane %d add %d: %v", i, j, err)
					return
				}
				if err := g.WorktreeRemove(ctx, clone, wt); err != nil {
					t.Errorf("lane %d remove %d: %v", i, j, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestIsAncestorAndParents(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, clone := gittest.NewRemote(t)
	commit := func(name string) string {
		if err := os.WriteFile(filepath.Join(clone, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		gittest.Run(t, clone, "add", name)
		gittest.Run(t, clone, "commit", "-q", "-m", name)
		return strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	}
	base := commit("a")
	gittest.Run(t, clone, "checkout", "-q", "-b", "side")
	side := commit("b")
	gittest.Run(t, clone, "checkout", "-q", "-")
	other := commit("c")
	gittest.Run(t, clone, "merge", "-q", "--no-ff", "-m", "merge", "side")
	merge := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	for _, tc := range []struct {
		a, b string
		want bool
	}{{base, side, true}, {side, side, true}, {side, other, false}, {side, merge, true}, {merge, side, false}} {
		got, err := g.IsAncestor(ctx, clone, tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Errorf("IsAncestor(%.7s, %.7s) = %v, %v; want %v", tc.a, tc.b, got, err, tc.want)
		}
	}
	if _, err := g.IsAncestor(ctx, clone, "nope", side); err == nil {
		t.Error("want error for an unknown commit")
	}
	ps, err := g.Parents(ctx, clone, merge)
	if err != nil || len(ps) != 2 {
		t.Errorf("merge parents = %v, %v", ps, err)
	}
	if ps, err = g.Parents(ctx, clone, other); err != nil || len(ps) != 1 || ps[0] != base {
		t.Errorf("parents = %v, %v", ps, err)
	}
}
