package gitx

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// The functions in this file read two commits of the controller's own
// repository (never an agent's worktree) and change nothing.

var excludePrefix = regexp.MustCompile(`^[.A-Za-z0-9_/-]+/$`)

// diffArgs builds the arguments shared by Diff and AddedLinesBetween. Textconv
// and external drivers are off and attributes are read from the empty tree, so
// nothing in the commits (a .gitattributes entry such as -diff or binary) can
// hide a change. Each exclude prefix becomes a top-level exclude pathspec.
func diffArgs(gitDir string, from, to string, exclude []string, mode ...string) ([]string, error) {
	args := []string{"--git-dir=" + gitDir,
		"-c", "core.quotePath=false", "-c", "diff.external=", "-c", "core.autocrlf=false",
		"diff", "--no-color", "--no-ext-diff", "--no-textconv", "--text", "-M",
		"--src-prefix=a/", "--dst-prefix=b/"}
	args = append(args, mode...)
	args = append(args, from, to, "--")
	for _, p := range exclude {
		if !excludePrefix.MatchString(p) || strings.Contains(p, "..") || strings.HasPrefix(p, "/") || strings.Contains(p, "//") {
			return nil, fmt.Errorf("gitx: bad exclude prefix %q", p)
		}
		args = append(args, ":(top,exclude)"+p)
	}
	if len(exclude) == 0 {
		args = append(args, ":(top)")
	}
	return args, nil
}

// Diff returns git diff --stat --patch from to (with --text, --no-textconv,
// --no-ext-diff and -M), leaving out paths under each exclude prefix
// (repo-relative, ending in "/"), cut to maxBytes at a line end. truncated
// reports a cut. repoDir is the controller's clone and may be bare.
func (g *Git) Diff(ctx context.Context, repoDir, from, to string, maxBytes int64, exclude ...string) (patch []byte, truncated bool, err error) {
	args, err := diffArgs(gitDirOf(repoDir), from, to, exclude, "--stat", "--patch")
	if err != nil {
		return nil, false, err
	}
	out, _, err := g.run(ctx, repoDir, []string{"GIT_ATTR_SOURCE=" + emptyTree, "GIT_OPTIONAL_LOCKS=0"}, "", nil, args...)
	if err != nil {
		return nil, false, fmt.Errorf("gitx: diff %.12s %.12s: %w", from, to, err)
	}
	if maxBytes > 0 && int64(len(out)) > maxBytes {
		cut := out[:maxBytes]
		if i := strings.LastIndex(cut, "\n"); i >= 0 {
			cut = cut[:i+1]
		}
		return []byte(cut), true, nil
	}
	return []byte(out), false, nil
}

// AddedLinesBetween is the head-side line ranges each path gained from from to
// to (git diff -U0 -M --text), leaving out the exclude prefixes. A renamed
// file is listed under its new path with only its edited lines.
func (g *Git) AddedLinesBetween(ctx context.Context, repoDir, from, to string, exclude ...string) (map[string][]LineRange, error) {
	args, err := diffArgs(gitDirOf(repoDir), from, to, exclude, "-U0")
	if err != nil {
		return nil, err
	}
	out, _, err := g.run(ctx, repoDir, []string{"GIT_ATTR_SOURCE=" + emptyTree, "GIT_OPTIONAL_LOCKS=0"}, "", nil, args...)
	if err != nil {
		return nil, fmt.Errorf("gitx: added lines %.12s %.12s: %w", from, to, err)
	}
	return parseAdded(out)
}

// CodeChanged lists the paths that differ between commits from and to, leaving
// out the exclude prefixes. A moved PR head whose only new commits touch an
// excluded prefix (the .ard/ close-out commits) gives none.
func (g *Git) CodeChanged(ctx context.Context, repoDir, from, to string, exclude ...string) ([]string, error) {
	args, err := diffArgs(gitDirOf(repoDir), from, to, exclude, "--name-only", "-z", "--no-renames")
	if err != nil {
		return nil, err
	}
	out, _, err := g.run(ctx, repoDir, []string{"GIT_ATTR_SOURCE=" + emptyTree, "GIT_OPTIONAL_LOCKS=0"}, "", nil, args...)
	if err != nil {
		return nil, fmt.Errorf("gitx: code changed %.12s %.12s: %w", from, to, err)
	}
	var ps []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			ps = append(ps, p)
		}
	}
	return ps, nil
}
