package gitx

import (
	"context"
	"fmt"
	"strings"
)

// emptyTreeID returns the id of the empty tree, computed rather than
// hard-coded so it follows the repository's hash algorithm.
func (g *Git) emptyTreeID(ctx context.Context, dir string) (string, error) {
	out, _, err := g.run(ctx, dir, nil, "", nil, "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", fmt.Errorf("gitx: empty tree: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// hardenedMerge are the options every merge runs with: nothing in the
// repository's own configuration or attributes can change its result.
var hardenedMerge = []string{
	"-c", "rerere.enabled=false", "-c", "merge.renormalize=false",
	"-c", "submodule.recurse=false", "-c", "core.autocrlf=false",
	"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null",
}

// MergeNoCommit merges ref into the worktree at dir without committing and
// returns the conflicted paths. A clean merge returns none. A failure that
// leaves no unmerged path is an error. The merge ignores every attribute file
// (GIT_ATTR_SOURCE is the empty tree), takes no -X options and runs no hooks.
func (g *Git) MergeNoCommit(ctx context.Context, dir, ref string) ([]string, error) {
	empty, err := g.emptyTreeID(ctx, dir)
	if err != nil {
		return nil, err
	}
	args := append(append([]string{}, hardenedMerge...),
		"merge", "--no-commit", "--no-ff", "--no-verify", "--no-edit", "-s", "ort", ref)
	_, _, mergeErr := g.run(ctx, dir, []string{"GIT_ATTR_SOURCE=" + empty,
		"GIT_AUTHOR_NAME=ard", "GIT_AUTHOR_EMAIL=ard@localhost",
		"GIT_COMMITTER_NAME=ard", "GIT_COMMITTER_EMAIL=ard@localhost"}, "", nil, args...)
	if mergeErr == nil {
		return nil, nil
	}
	if !isExit(mergeErr, 1) {
		return nil, fmt.Errorf("gitx: merge %s: %w", ref, mergeErr)
	}
	out, _, err := g.run(ctx, dir, nil, "", nil, "diff", "--name-only", "-z", "--diff-filter=U")
	if err != nil {
		return nil, fmt.Errorf("gitx: list conflicts: %w", err)
	}
	var ps []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			ps = append(ps, p)
		}
	}
	if len(ps) == 0 {
		return nil, fmt.Errorf("gitx: merge %s: %w", ref, mergeErr)
	}
	return ps, nil
}

// MergeAbort abandons a merge started by MergeNoCommit.
func (g *Git) MergeAbort(ctx context.Context, dir string) error {
	if _, _, err := g.run(ctx, dir, nil, "", nil, "merge", "--abort"); err != nil {
		return fmt.Errorf("gitx: merge abort: %w", err)
	}
	return nil
}
