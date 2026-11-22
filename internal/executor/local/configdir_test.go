//go:build unix

package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
)

func TestPrepareCreatesConfigDir(t *testing.T) {
	old := syscall.Umask(0) // umask 0: only the explicit chmod can give 0700
	defer syscall.Umask(old)
	f := newFixture(t)
	h, err := f.l.Prepare(context.Background(), f.job(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	defer f.l.Cleanup(context.Background(), h, executor.CleanupOptions{})
	want := filepath.Join(h.Home, ".claude")
	if h.View.ConfigDir != want {
		t.Fatalf("View.ConfigDir = %q, want %q", h.View.ConfigDir, want)
	}
	st, err := os.Lstat(want)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Fatalf("config dir: %v %v", st, err)
	}
	if ents, _ := os.ReadDir(want); len(ents) != 0 {
		t.Fatalf("not empty: %v", ents)
	}
}

func TestPrepareFailureRemovesConfigDir(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	f.l.now = func() time.Time { return now }
	j := f.job(time.Minute)
	logDir, _ := f.l.logDir(j.Repo, j.Issue)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A pre-created log makes the O_EXCL create fail after home exists.
	logf := filepath.Join(logDir, LogBase(j.Stage, j.Role, now)+".log")
	if err := os.WriteFile(logf, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.l.Prepare(context.Background(), j); err == nil {
		t.Fatal("Prepare succeeded")
	}
	home, _ := f.l.homePath(j.Repo, j.ID)
	for _, p := range []string{home, filepath.Join(home, ".claude")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s remains: %v", p, err)
		}
	}
}

func TestCleanupRemovesConfigDir(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h, _ := f.run(t, f.job(time.Minute), agent(t, "ok"))
	if _, err := os.Stat(h.View.ConfigDir); err != nil {
		t.Fatal(err)
	}
	if err := f.l.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{h.View.ConfigDir, h.Home} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s remains: %v", p, err)
		}
	}
}

func TestStartConfigDirChecks(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, h *executor.Handle) []string // returns Fixed
		want  error
	}{
		{"fixed elsewhere", func(t *testing.T, h *executor.Handle) []string { return []string{"CLAUDE_CONFIG_DIR=/elsewhere"} }, executor.ErrForbiddenEnv},
		{"fixed on handle without dir", func(t *testing.T, h *executor.Handle) []string {
			h.View.ConfigDir = ""
			return []string{"CLAUDE_CONFIG_DIR=/x"}
		}, executor.ErrForbiddenEnv},
		{"symlink", func(t *testing.T, h *executor.Handle) []string {
			p := filepath.Join(h.Home, ".claude")
			os.Remove(p)
			if err := os.Symlink(t.TempDir(), p); err != nil {
				t.Fatal(err)
			}
			return nil
		}, executor.ErrNotPrepared},
		{"mode 0755", func(t *testing.T, h *executor.Handle) []string {
			if err := os.Chmod(filepath.Join(h.Home, ".claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			return nil
		}, executor.ErrNotPrepared},
		{"holds a file", func(t *testing.T, h *executor.Handle) []string {
			if err := os.WriteFile(filepath.Join(h.Home, ".claude", ".credentials.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			return nil
		}, executor.ErrNotPrepared},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			h, err := f.l.Prepare(ctx, f.job(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			c := agent(t, "ok")
			c.Fixed = tc.setup(t, &h)
			_, err = f.l.Start(ctx, h, c)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Start err = %v, want %v", err, tc.want)
			}
			// Cleanup after the failed Start removes the dir and home.
			if err := f.l.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{filepath.Join(h.Home, ".claude"), h.Home} {
				if _, err := os.Lstat(p); !os.IsNotExist(err) {
					t.Errorf("%s remains: %v", p, err)
				}
			}
		})
	}
}

func TestStartForbiddenEnvThenCleanup(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h, err := f.l.Prepare(ctx, f.job(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.l.Start(ctx, h, agent(t, "ok", "GITHUB_TOKEN=x")); !errors.Is(err, executor.ErrForbiddenEnv) {
		t.Fatalf("Start err = %v", err)
	}
	if err := f.l.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(h.View.ConfigDir); !os.IsNotExist(err) {
		t.Errorf("config dir remains: %v", err)
	}
}

func TestConfigDirReachesProcess(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h, err := f.l.Prepare(ctx, f.job(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	defer f.l.Cleanup(ctx, h, executor.CleanupOptions{})
	c := agent(t, "env")
	c.Fixed = []string{"CLAUDE_CONFIG_DIR=" + h.View.ConfigDir}
	if h, err = f.l.Start(ctx, h, c); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := executor.WaitFor(wctx, f.l, h, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	out := "\n" + readFile(t, h.StdoutPath)
	for _, w := range []string{"CLAUDE_CONFIG_DIR=" + h.View.ConfigDir} {
		if !strings.Contains(out, "\n"+w+"\n") {
			t.Errorf("%s missing from env:\n%s", w, out)
		}
	}
}

// Both credentials are set in the controller, but the job gets only the one
// in Command.Env: the CLI prefers the API key, so leaking it would win.
func TestExactlyOneCredentialReachesProcess(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "controller-api-key-0001")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "controller-token-0002")
	f := newFixture(t)
	f.l.allowEnv["CLAUDE_CODE_OAUTH_TOKEN"] = true
	h, _ := f.run(t, f.job(time.Minute), agent(t, "env", "CLAUDE_CODE_OAUTH_TOKEN=job-token-value-0003"))
	out := "\n" + readFile(t, h.StdoutPath)
	if !strings.Contains(out, "\nCLAUDE_CODE_OAUTH_TOKEN=job-token-value-0003\n") {
		t.Errorf("token missing:\n%s", out)
	}
	if strings.Contains(out, "ANTHROPIC_API_KEY") || strings.Contains(out, "controller-") {
		t.Errorf("controller credential leaked:\n%s", out)
	}
}
