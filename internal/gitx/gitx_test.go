package gitx

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
