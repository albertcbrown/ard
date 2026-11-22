package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/wtlock"
)

// worktreeAdminTimeout bounds each worktree add, remove and prune that runs
// under the shared lock, so one hung git cannot stall every other job.
const worktreeAdminTimeout = 30 * time.Second

// gitRunner is the small slice of git the executor needs. It is unexported
// so this package does not depend on a shared git package.
type gitRunner interface {
	RevParse(ctx context.Context, repo, rev string) (string, error)
	// WorktreeAdd creates a detached worktree at dir, at sha.
	WorktreeAdd(ctx context.Context, repo, dir, sha string) error
	WorktreeRemove(ctx context.Context, repo, dir string, force bool) error
	WorktreePrune(ctx context.Context, repo string) error
}

type execGit struct{ bin string }

func (g execGit) command(ctx context.Context, repo string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, g.bin, append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-C", repo}, args...)...)
	// Never HOME=repo: git would read <repo>/.gitconfig, which a branch controls.
	cmd.Env = []string{"PATH=" + defaultPath(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null", "HOME=" + os.TempDir()}
	return cmd
}

func (g execGit) run(ctx context.Context, repo string, args ...string) (string, error) {
	out, err := g.command(ctx, repo, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// out is run with stdout only, for output that is parsed.
func (g execGit) out(ctx context.Context, repo string, args ...string) (string, error) {
	out, err := g.command(ctx, repo, args...).Output()
	if err != nil {
		var ee *exec.ExitError
		msg := ""
		if errors.As(err, &ee) {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// lock takes the process-wide worktree-administration lock for repo, keyed by
// its common git directory (shared with gitx via internal/wtlock).
func (g execGit) lock(ctx context.Context, repo string) (func(), error) {
	dir, err := g.out(ctx, repo, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil, fmt.Errorf("worktree lock %s: %w", repo, err)
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repo, dir)
	}
	unlock, err := wtlock.Lock(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("worktree lock %s: %w", repo, err)
	}
	return unlock, nil
}

// admin runs one worktree add, remove or prune under the lock with a deadline.
func (g execGit) admin(ctx context.Context, repo string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, worktreeAdminTimeout)
	defer cancel()
	unlock, err := g.lock(ctx, repo)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = g.run(ctx, repo, args...)
	return err
}

func (g execGit) RevParse(ctx context.Context, repo, rev string) (string, error) {
	return g.run(ctx, repo, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
}

func (g execGit) WorktreeAdd(ctx context.Context, repo, dir, sha string) error {
	return g.admin(ctx, repo, "worktree", "add", "--detach", dir, sha)
}

func (g execGit) WorktreeRemove(ctx context.Context, repo, dir string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	return g.admin(ctx, repo, append(args, dir)...)
}

func (g execGit) WorktreePrune(ctx context.Context, repo string) error {
	return g.admin(ctx, repo, "worktree", "prune")
}
