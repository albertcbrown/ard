package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

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

func (g execGit) run(ctx context.Context, repo string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, g.bin, append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-C", repo}, args...)...)
	// Never HOME=repo: git would read <repo>/.gitconfig, which a branch controls.
	cmd.Env = []string{"PATH=" + defaultPath(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null", "HOME=" + os.TempDir()}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (g execGit) RevParse(ctx context.Context, repo, rev string) (string, error) {
	return g.run(ctx, repo, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
}

func (g execGit) WorktreeAdd(ctx context.Context, repo, dir, sha string) error {
	_, err := g.run(ctx, repo, "worktree", "add", "--detach", dir, sha)
	return err
}

func (g execGit) WorktreeRemove(ctx context.Context, repo, dir string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	_, err := g.run(ctx, repo, append(args, dir)...)
	return err
}

func (g execGit) WorktreePrune(ctx context.Context, repo string) error {
	_, err := g.run(ctx, repo, "worktree", "prune")
	return err
}
