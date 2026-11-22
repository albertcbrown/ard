package gitx

import (
	"context"
	"fmt"
	"strings"
)

// The functions in this file are reads: they inspect a worktree or list refs
// on a remote and change nothing, so they are not effects.

// LsRemote lists the refs of the remote url that match pattern (a git
// ls-remote pattern such as "refs/heads/worker/eng-12-*") as ref -> SHA. A 403
// or 404 from the remote is ErrRepoAccess. token may be nil.
func (g *Git) LsRemote(ctx context.Context, dir, url, pattern string, token TokenFunc) (map[string]string, error) {
	var env, secrets []string
	if token != nil {
		tok, err := token(ctx)
		if err != nil {
			return nil, fmt.Errorf("gitx: ls-remote token: %w", err)
		}
		env, secrets = pushEnv(tok, url), []string{tok}
	}
	out, stderr, err := g.run(ctx, dir, env, "", secrets, "ls-remote", url, pattern)
	if err != nil {
		if classifyFetch(stderr, true) == ErrRepoAccess {
			return nil, fmt.Errorf("gitx: ls-remote %s: %w", pattern, ErrRepoAccess)
		}
		return nil, fmt.Errorf("gitx: ls-remote %s: %w", pattern, err)
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && sha != "" && ref != "" {
			refs[ref] = sha
		}
	}
	return refs, nil
}

// NumStat is one entry of git diff --numstat. Binary files have no line
// counts: Binary is set and Added and Deleted are zero.
type NumStat struct {
	Path           string
	Added, Deleted int
	Binary         bool
}

// LineRange is an inclusive range of 1-based line numbers.
type LineRange struct{ From, To int }

// MergeBase returns the best common ancestor of commits a and b in the
// repository at repoDir (the controller's own clone; never a job worktree).
func (g *Git) MergeBase(ctx context.Context, repoDir, a, b string) (string, error) {
	out, _, err := g.run(ctx, repoDir, nil, "", nil, "--git-dir="+gitDirOf(repoDir), "merge-base", a, b)
	if err != nil {
		return "", fmt.Errorf("gitx: merge-base %s %s: %w", a, b, err)
	}
	return strings.TrimSpace(out), nil
}
