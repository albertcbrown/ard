package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The functions in this file only touch the local repository or read from a
// remote. They are not effects: commit and push stay in write.go and are
// reachable only through internal/effects.

// ErrRefNotFound means a fetch named a remote ref that does not exist.
var ErrRefNotFound = errors.New("gitx: remote ref not found")

// Change is one entry of the staged diff.
type Change struct {
	Path    string
	NewMode string // "000000" for a deletion
	Status  byte   // A, M, D, T ...
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)`)

// Version returns the major and minor version of the git binary.
func (g *Git) Version(ctx context.Context) (major, minor int, err error) {
	out, _, err := g.run(ctx, "", nil, "", nil, "version")
	if err != nil {
		return 0, 0, fmt.Errorf("gitx: version: %w", err)
	}
	m := versionRe.FindStringSubmatch(out)
	if m == nil {
		return 0, 0, fmt.Errorf("gitx: unparseable version %q", strings.TrimSpace(out))
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, nil
}

// WorktreeAdd checks ref out detached at path. Detached, because the branch
// may already be checked out elsewhere and git refuses a second checkout.
func (g *Git) WorktreeAdd(ctx context.Context, repoDir, path, ref string) error {
	if _, _, err := g.run(ctx, repoDir, nil, "", nil, "worktree", "add", "--detach", "--force", path, ref); err != nil {
		return fmt.Errorf("gitx: worktree add %s: %w", path, err)
	}
	return nil
}

// WorktreeRemove deletes the worktree at path, whatever state it is in, and
// prunes stale registrations. A missing worktree is not an error.
func (g *Git) WorktreeRemove(ctx context.Context, repoDir, path string) error {
	_, _, _ = g.run(ctx, repoDir, nil, "", nil, "worktree", "remove", "--force", path)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("gitx: remove %s: %w", path, err)
	}
	if _, _, err := g.run(ctx, repoDir, nil, "", nil, "worktree", "prune"); err != nil {
		return fmt.Errorf("gitx: worktree prune: %w", err)
	}
	return nil
}

// FetchRef fetches refspec from url into repoDir. token may be nil. A remote
// ref that does not exist returns ErrRefNotFound.
func (g *Git) FetchRef(ctx context.Context, repoDir, url, refspec string, token TokenFunc) error {
	var env, secrets []string
	if token != nil {
		tok, err := token(ctx)
		if err != nil {
			return fmt.Errorf("gitx: fetch token: %w", err)
		}
		env, secrets = pushEnv(tok, url), []string{tok}
	}
	_, stderr, err := g.run(ctx, repoDir, env, "", secrets, "fetch", "--no-tags", "--no-write-fetch-head", "--quiet", url, refspec)
	if err != nil {
		if strings.Contains(stderr, "couldn't find remote ref") {
			return fmt.Errorf("gitx: fetch %s: %w", refspec, ErrRefNotFound)
		}
		return fmt.Errorf("gitx: fetch %s: %w", refspec, err)
	}
	return nil
}

// ResetHard moves the worktree, index and HEAD to ref.
func (g *Git) ResetHard(ctx context.Context, dir, ref string) error {
	if _, _, err := g.run(ctx, dir, nil, "", nil, "reset", "--hard", "--quiet", ref); err != nil {
		return fmt.Errorf("gitx: reset %s: %w", ref, err)
	}
	return nil
}

// CleanAll removes untracked and ignored files and nested repositories.
func (g *Git) CleanAll(ctx context.Context, dir string) error {
	if _, _, err := g.run(ctx, dir, nil, "", nil, "clean", "-ffdxq"); err != nil {
		return fmt.Errorf("gitx: clean: %w", err)
	}
	return nil
}

// AddForce stages paths even when .gitignore matches them.
func (g *Git) AddForce(ctx context.Context, dir string, paths ...string) error {
	args := append([]string{"--literal-pathspecs", "add", "-f", "--"}, paths...)
	if _, _, err := g.run(ctx, dir, nil, "", nil, args...); err != nil {
		return fmt.Errorf("gitx: add: %w", err)
	}
	return nil
}

// RmCached stages the removal of paths; an untracked path is ignored.
func (g *Git) RmCached(ctx context.Context, dir string, paths ...string) error {
	args := append([]string{"--literal-pathspecs", "rm", "-q", "--cached", "--ignore-unmatch", "--"}, paths...)
	if _, _, err := g.run(ctx, dir, nil, "", nil, args...); err != nil {
		return fmt.Errorf("gitx: rm: %w", err)
	}
	return nil
}

// StagedChanges lists the staged diff with new modes, without rename
// detection.
func (g *Git) StagedChanges(ctx context.Context, dir string) ([]Change, error) {
	out, _, err := g.run(ctx, dir, nil, "", nil, "diff", "--cached", "--raw", "-z", "--no-renames")
	if err != nil {
		return nil, fmt.Errorf("gitx: staged changes: %w", err)
	}
	tok := strings.Split(out, "\x00")
	var cs []Change
	for i := 0; i+1 < len(tok); i += 2 {
		meta := strings.Fields(strings.TrimPrefix(tok[i], ":")) // oldmode newmode oldsha newsha status
		if len(meta) < 5 {
			return nil, fmt.Errorf("gitx: staged changes: bad record %q", tok[i])
		}
		cs = append(cs, Change{Path: tok[i+1], NewMode: meta[1], Status: meta[4][0]})
	}
	return cs, nil
}

// ChangedPaths lists the paths among paths that differ between from and to.
func (g *Git) ChangedPaths(ctx context.Context, dir, from, to string, paths ...string) ([]string, error) {
	args := append([]string{"--literal-pathspecs", "diff", "--name-only", "-z", from, to, "--"}, paths...)
	out, _, err := g.run(ctx, dir, nil, "", nil, args...)
	if err != nil {
		return nil, fmt.Errorf("gitx: changed paths: %w", err)
	}
	var ps []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			ps = append(ps, p)
		}
	}
	return ps, nil
}
