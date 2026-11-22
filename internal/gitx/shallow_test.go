package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

func TestInitBareIdempotent(t *testing.T) {
	ctx := context.Background()
	g := New("")
	dir := filepath.Join(t.TempDir(), "a", "b.git")
	for i := 0; i < 2; i++ {
		if err := g.InitBare(ctx, dir); err != nil {
			t.Fatalf("InitBare #%d: %v", i, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		t.Error(err)
	}
	if out := gittest.Run(t, dir, "rev-parse", "--is-bare-repository"); strings.TrimSpace(out) != "true" {
		t.Errorf("is-bare = %q", out)
	}
}

func TestFetchShallow(t *testing.T) {
	ctx := context.Background()
	g := New("")
	bare, clone := gittest.NewRemote(t)
	older := commitFiles(t, clone, map[string]fileSpec{"spec.md": {"a", 0o644}})
	newer := commitFiles(t, clone, map[string]fileSpec{"spec.md": {"b", 0o644}})
	gittest.Run(t, clone, "push", "origin", "main")

	tests := []struct {
		name    string
		refspec string
		want    string // expected sha at refs/x; "" when an error is expected
		wantErr error
	}{
		{"branch", "+main:refs/x", newer, nil},
		{"older sha", "+" + older + ":refs/x", older, nil},
		{"unknown branch", "+nope:refs/x", "", ErrRefNotFound},
		{"unknown sha", "+" + strings.Repeat("1", 40) + ":refs/x", "", ErrRefNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cache := filepath.Join(t.TempDir(), "c.git")
			if err := g.InitBare(ctx, cache); err != nil {
				t.Fatal(err)
			}
			err := g.FetchShallow(ctx, cache, bare, tc.refspec, nil)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v; want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := g.RevParse(ctx, cache, "refs/x"); got != tc.want {
				t.Errorf("refs/x = %s; want %s", got, tc.want)
			}
			if _, err := os.Stat(filepath.Join(cache, "shallow")); err != nil {
				t.Errorf("no shallow file: %v", err)
			}
		})
	}

	t.Run("token never in error", func(t *testing.T) {
		cache := filepath.Join(t.TempDir(), "c.git")
		if err := g.InitBare(ctx, cache); err != nil {
			t.Fatal(err)
		}
		tok := func(context.Context) (string, error) { return "s3cr3t-tok", nil }
		err := g.FetchShallow(ctx, cache, filepath.Join(t.TempDir(), "missing.git"), "+main:refs/x", tok)
		if err == nil {
			t.Fatal("want error")
		}
		if errors.Is(err, ErrRefNotFound) {
			t.Errorf("a missing remote is not ErrRefNotFound: %v", err)
		}
		if strings.Contains(err.Error(), "s3cr3t-tok") {
			t.Errorf("token leaked: %v", err)
		}
	})
}

func TestExportTree(t *testing.T) {
	ctx := context.Background()
	g := New("")

	t.Run("nested files, exec bit, no .git", func(t *testing.T) {
		_, c := gittest.NewRemote(t)
		sha := commitFiles(t, c, map[string]fileSpec{
			"README.md":      {"c:README.md", 0o644},
			"docs/a/b.md":    {"c:docs/a/b.md", 0o644},
			"bin/run.sh":     {"c:bin/run.sh", 0o755},
			".ard-x/hidden":  {"c:.ard-x/hidden", 0o644},
			"top/.git-notes": {"c:top/.git-notes", 0o644},
		})
		dest := filepath.Join(t.TempDir(), "d")
		if err := g.ExportTree(ctx, c, sha, dest); err != nil {
			t.Fatal(err)
		}
		want := map[string]os.FileMode{"README.md": 0o644, "docs/a/b.md": 0o644, "bin/run.sh": 0o755, ".ard-x/hidden": 0o644, "top/.git-notes": 0o644}
		for rel, mode := range want {
			p := filepath.Join(dest, filepath.FromSlash(rel))
			st, err := os.Stat(p)
			if err != nil {
				t.Errorf("%s: %v", rel, err)
				continue
			}
			if st.Mode().Perm() != mode {
				t.Errorf("%s mode = %v; want %v", rel, st.Mode().Perm(), mode)
			}
			if b, _ := os.ReadFile(p); string(b) != "c:"+rel {
				t.Errorf("%s content = %q", rel, b)
			}
		}
		if _, err := os.Lstat(filepath.Join(dest, ".git")); !os.IsNotExist(err) {
			t.Errorf(".git present: %v", err)
		}
	})

	t.Run("symlink is rejected", func(t *testing.T) {
		_, c := gittest.NewRemote(t)
		if err := os.Symlink("README.md", filepath.Join(c, "link")); err != nil {
			t.Fatal(err)
		}
		sha := commitFiles(t, c, map[string]fileSpec{"README.md": {"x", 0o644}})
		dest := filepath.Join(t.TempDir(), "d")
		err := g.ExportTree(ctx, c, sha, dest)
		if err == nil || !strings.Contains(err.Error(), "link") || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Lstat(dest); !os.IsNotExist(err) {
			t.Errorf("dest left behind: %v", err)
		}
	})

	t.Run("submodule is rejected", func(t *testing.T) {
		_, c := gittest.NewRemote(t)
		s := commitFiles(t, c, map[string]fileSpec{"README.md": {"x", 0o644}})
		gittest.Run(t, c, "update-index", "--add", "--cacheinfo", "160000,"+s+",sub")
		gittest.Run(t, c, "commit", "-m", "sub")
		head, _ := g.RevParse(ctx, c, "HEAD")
		dest := filepath.Join(t.TempDir(), "d")
		if err := g.ExportTree(ctx, c, head, dest); err == nil || !strings.Contains(err.Error(), "submodule") {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Lstat(dest); !os.IsNotExist(err) {
			t.Errorf("dest left behind: %v", err)
		}
	})

	t.Run("dest exists", func(t *testing.T) {
		_, c := gittest.NewRemote(t)
		sha := commitFiles(t, c, map[string]fileSpec{"README.md": {"x", 0o644}})
		if err := g.ExportTree(ctx, c, sha, t.TempDir()); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("size cap", func(t *testing.T) {
		old := maxExportBytes
		maxExportBytes = 10
		defer func() { maxExportBytes = old }()
		_, c := gittest.NewRemote(t)
		s := commitFiles(t, c, map[string]fileSpec{"big.md": {"12345678901", 0o644}})
		dest := filepath.Join(t.TempDir(), "d")
		if err := g.ExportTree(ctx, c, s, dest); err == nil || !strings.Contains(err.Error(), "MiB") {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Lstat(dest); !os.IsNotExist(err) {
			t.Errorf("dest left behind: %v", err)
		}
	})
}

func TestClassifyFetch(t *testing.T) {
	tests := []struct {
		stderr  string
		shallow bool
		want    error
	}{
		{"remote: Repository not found.\nfatal: repository 'x' not found", true, ErrRepoAccess},
		{"fatal: repository 'https://github.com/a/b.git/' not found", true, ErrRepoAccess},
		{"fatal: unable to access 'u': The requested URL returned error: 403", true, ErrRepoAccess},
		{"fatal: unable to access 'u': The requested URL returned error: 404", true, ErrRepoAccess},
		{"fatal: couldn't find remote ref nope", true, ErrRefNotFound},
		{"fatal: couldn't find remote ref nope", false, ErrRefNotFound},
		{"fatal: not our ref abc", true, ErrRefNotFound},
		{"fatal: not our ref abc", false, nil},
		{"The requested URL returned error: 404", false, nil},
		{"fatal: Could not resolve host", true, nil},
		{"fatal: '/tmp/x/missing.git' does not appear to be a git repository\nfatal: Could not read from remote repository.", true, nil},
		{"fatal: unable to access 'https://nope.invalid/a/b.git/': Could not resolve host: nope.invalid", true, nil},
		{"fatal: unable to access 'https://localhost:1/a.git/': Failed to connect to localhost port 1: Connection refused", true, nil},
		{"fatal: could not read Username for 'https://github.com': terminal prompts disabled", true, nil},
	}
	for _, tc := range tests {
		if got := classifyFetch(tc.stderr, tc.shallow); got != tc.want {
			t.Errorf("classifyFetch(%q, %v) = %v; want %v", tc.stderr, tc.shallow, got, tc.want)
		}
	}
}
