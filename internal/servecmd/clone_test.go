package servecmd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/repopath"
)

var testRepo = github.Repo{Owner: "Owner", Name: "Repo"}

func TestClonePath(t *testing.T) {
	got, err := clonePath("/h", testRepo)
	if err != nil || got != "/h/.ard/repos/owner/repo.git" {
		t.Fatalf("clonePath = %q, %v", got, err)
	}
	lane, err := laneWorkDir("/h", testRepo)
	if err != nil || lane != "/h/.ard/work/owner/repo/lanes" {
		t.Fatalf("laneWorkDir = %q, %v", lane, err)
	}
	bad := github.Repo{Owner: "..", Name: "x"}
	if _, err := clonePath("/h", bad); !errors.Is(err, repopath.ErrInvalid) {
		t.Errorf("clonePath err = %v, want ErrInvalid", err)
	}
	if _, err := laneWorkDir("/h", bad); !errors.Is(err, repopath.ErrInvalid) {
		t.Errorf("laneWorkDir err = %v, want ErrInvalid", err)
	}
}

func cloneLog() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(buf, nil)), buf
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(gittest.Run(t, dir, args...))
}

// TestOpenClone runs each openClone scenario against a fresh HOME.
func TestOpenClone(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"fresh clone", func(t *testing.T) {
			home := t.TempDir()
			bare, _ := gittest.NewRemote(t)
			log, buf := cloneLog()
			p, err := openClone(context.Background(), gitx.New(""), home, testRepo, "file://"+bare, nil, log)
			if err != nil {
				t.Fatal(err)
			}
			if want, _ := clonePath(home, testRepo); p != want {
				t.Fatalf("path = %q, want %q", p, want)
			}
			if got := gitOut(t, p, "--git-dir=.", "rev-parse", "--is-bare-repository"); got != "true" {
				t.Errorf("is-bare = %q", got)
			}
			if got := gitOut(t, p, "config", "--get", "gc.auto"); got != "0" {
				t.Errorf("gc.auto = %q", got)
			}
			if got := gitOut(t, p, "config", "--get", "maintenance.auto"); got != "false" {
				t.Errorf("maintenance.auto = %q", got)
			}
			cfg, err := os.ReadFile(filepath.Join(p, "config"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(cfg), "[remote") || strings.Contains(string(cfg), "[http") {
				t.Errorf("remote or http config stored:\n%s", cfg)
			}
			if got, want := gitOut(t, p, "rev-parse", seedRef), gitOut(t, bare, "rev-parse", "main"); got != want {
				t.Errorf("seed = %s, want %s", got, want)
			}
			if _, err := os.Stat(p + ".tmp-init"); !os.IsNotExist(err) {
				t.Error(".tmp-init left behind")
			}
			if !strings.Contains(buf.String(), "serve.clone_created") || !strings.Contains(buf.String(), "serve.clone_seeded") {
				t.Errorf("log: %s", buf)
			}
		}},
		{"second call reuses", func(t *testing.T) {
			home := t.TempDir()
			bare, clone := gittest.NewRemote(t)
			url := "file://" + bare
			ctx := context.Background()
			log, buf := cloneLog()
			p, err := openClone(ctx, gitx.New(""), home, testRepo, url, nil, log)
			if err != nil {
				t.Fatal(err)
			}
			seed := gitOut(t, p, "rev-parse", seedRef)
			gittest.Run(t, p, "update-ref", "refs/ard/marker", seed)
			gittest.Run(t, clone, "commit", "--allow-empty", "-m", "more")
			gittest.Run(t, clone, "push", "origin", "main")
			buf.Reset()
			if _, err := openClone(ctx, gitx.New(""), home, testRepo, url, nil, log); err != nil {
				t.Fatal(err)
			}
			if got := gitOut(t, p, "rev-parse", seedRef); got != seed {
				t.Errorf("re-seeded: %s -> %s", seed, got)
			}
			if got := gitOut(t, p, "rev-parse", "refs/ard/marker"); got != seed {
				t.Errorf("marker lost: %s", got)
			}
			if strings.Contains(buf.String(), "serve.clone_created") {
				t.Errorf("second call created the clone: %s", buf)
			}
		}},
		{"leftover tmp-init", func(t *testing.T) {
			home := t.TempDir()
			bare, _ := gittest.NewRemote(t)
			p, _ := clonePath(home, testRepo)
			if err := os.MkdirAll(p+".tmp-init", 0o700); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(p+".tmp-init", "junk"), []byte("x"), 0o600)
			log, _ := cloneLog()
			if _, err := openClone(context.Background(), gitx.New(""), home, testRepo, "file://"+bare, nil, log); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(p + ".tmp-init"); !os.IsNotExist(err) {
				t.Error(".tmp-init remains")
			}
		}},
		{"foreign path", func(t *testing.T) {
			bare, clone := gittest.NewRemote(t)
			tests := []struct {
				name  string
				setup func(t *testing.T, p string)
			}{
				{"regular file", func(t *testing.T, p string) {
					if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
						t.Fatal(err)
					}
				}},
				{"symlink to a bare repo", func(t *testing.T, p string) {
					if err := os.Symlink(bare, p); err != nil {
						t.Fatal(err)
					}
				}},
				{"non-bare repo", func(t *testing.T, p string) {
					gittest.Run(t, filepath.Dir(p), "clone", "-q", clone, filepath.Base(p))
				}},
				{"empty directory", func(t *testing.T, p string) {
					if err := os.Mkdir(p, 0o700); err != nil {
						t.Fatal(err)
					}
				}},
			}
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					home := t.TempDir()
					// An empty dir under a git repo $HOME must not walk up into it.
					gittest.Run(t, home, "init", "-q")
					p, _ := clonePath(home, testRepo)
					if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
						t.Fatal(err)
					}
					tc.setup(t, p)
					before, _ := os.Lstat(p)
					log, _ := cloneLog()
					_, err := openClone(context.Background(), gitx.New(""), home, testRepo, "file://"+bare, nil, log)
					if err == nil || !strings.Contains(err.Error(), p) || !strings.Contains(err.Error(), "remove it") {
						t.Fatalf("err = %v", err)
					}
					after, aerr := os.Lstat(p)
					if aerr != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
						t.Errorf("path changed: %v", aerr)
					}
				})
			}
		}},
		{"seed failure is not fatal", func(t *testing.T) {
			home := t.TempDir()
			log, buf := cloneLog()
			missing := "file://" + filepath.Join(t.TempDir(), "nope.git")
			p, err := openClone(context.Background(), gitx.New(""), home, testRepo, missing, nil, log)
			if err != nil || p == "" {
				t.Fatalf("openClone = %q, %v", p, err)
			}
			if !strings.Contains(buf.String(), "serve.clone_seed_failed") {
				t.Errorf("no seed failure logged: %s", buf)
			}
			bare, _ := gittest.NewRemote(t)
			if _, err := openClone(context.Background(), gitx.New(""), home, testRepo, "file://"+bare, nil, log); err != nil {
				t.Fatal(err)
			}
			if got, want := gitOut(t, p, "rev-parse", seedRef), gitOut(t, bare, "rev-parse", "main"); got != want {
				t.Errorf("retry did not seed: %s", got)
			}
		}},
		{"cancelled during seed", func(t *testing.T) {
			home := t.TempDir()
			bare, _ := gittest.NewRemote(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			log, _ := cloneLog()
			if _, err := openClone(ctx, gitx.New(""), home, testRepo, "file://"+bare, nil, log); err == nil {
				t.Fatal("cancelled ctx accepted")
			}
		}},
		{"legacy lanes removed", func(t *testing.T) {
			home := t.TempDir()
			bare, _ := gittest.NewRemote(t)
			work, _ := repoWorkDir(home, testRepo)
			keep := []string{"01JABCDEFGHJKMNPQRSTVWXYZ0", "lanes"}
			for _, n := range append([]string{"committer-main", "committer-branch-x"}, keep...) {
				if err := os.MkdirAll(filepath.Join(work, n), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			log, _ := cloneLog()
			if _, err := openClone(context.Background(), gitx.New(""), home, testRepo, "file://"+bare, nil, log); err != nil {
				t.Fatal(err)
			}
			for _, n := range []string{"committer-main", "committer-branch-x"} {
				if _, err := os.Stat(filepath.Join(work, n)); !os.IsNotExist(err) {
					t.Errorf("%s remains", n)
				}
			}
			for _, n := range keep {
				if _, err := os.Stat(filepath.Join(work, n)); err != nil {
					t.Errorf("%s removed: %v", n, err)
				}
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
