// Package gitx wraps the git CLI for the controller. The exec runner is
// unexported on purpose: only this package may start a git process, and the
// mutating calls (Commit, Push) may be used only from internal/effects (see
// the architecture test in internal/effects/archtest).
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Identity is a git author/committer identity.
type Identity struct{ Name, Email string }

var (
	// ErrNothingToCommit means the index matches HEAD. Callers treat it as a
	// no-op, not a failure.
	ErrNothingToCommit = errors.New("gitx: nothing to commit")
	// ErrNonFastForward means the remote ref moved and the push was rejected.
	ErrNonFastForward = errors.New("gitx: push rejected: non-fast-forward")
	// ErrStaleLease means a --force-with-lease expectation did not hold.
	ErrStaleLease = errors.New("gitx: push rejected: stale lease")
)

// Git runs the git binary.
type Git struct{ bin string }

// New returns a Git using bin, or "git" when bin is empty.
func New(bin string) *Git {
	if bin == "" {
		bin = "git"
	}
	return &Git{bin: bin}
}

// runError carries stderr (already scrubbed) from a failed git command.
type runError struct {
	args   []string
	stderr string
	err    error
}

func (e *runError) Error() string {
	return fmt.Sprintf("gitx: git %s: %v: %s", strings.Join(e.args, " "), e.err, strings.TrimSpace(e.stderr))
}
func (e *runError) Unwrap() error { return e.err }

// run executes git in dir with extra env entries and optional stdin. Stderr
// is scrubbed of secrets before it appears in an error.
func (g *Git) run(ctx context.Context, dir string, env []string, stdin string, secrets []string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, g.bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	cmd.Env = append(cmd.Env, env...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		stderr := scrub(errb.String(), secrets...)
		return out.String(), stderr, &runError{args: scrubArgs(args, secrets), stderr: stderr, err: err}
	}
	return out.String(), scrub(errb.String(), secrets...), nil
}

func scrubArgs(args []string, secrets []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = scrub(a, secrets...)
	}
	return out
}

// StagedPaths lists the paths staged in dir's index.
func (g *Git) StagedPaths(ctx context.Context, dir string) ([]string, error) {
	out, _, err := g.run(ctx, dir, nil, "", nil, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil, fmt.Errorf("gitx: staged paths: %w", err)
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// RevParse resolves ref to a full SHA in dir.
func (g *Git) RevParse(ctx context.Context, dir, ref string) (string, error) {
	out, _, err := g.run(ctx, dir, nil, "", nil, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("gitx: rev-parse %s: %w", ref, err)
	}
	return strings.TrimSpace(out), nil
}

func isExit(err error, code int) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == code
}

// RemoteURL returns the fetch URL of the named remote in dir.
func (g *Git) RemoteURL(ctx context.Context, dir, remote string) (string, error) {
	out, _, err := g.run(ctx, dir, nil, "", nil, "remote", "get-url", remote)
	if err != nil {
		return "", fmt.Errorf("gitx: remote url %s: %w", remote, err)
	}
	return strings.TrimSpace(out), nil
}
