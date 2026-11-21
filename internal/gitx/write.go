package gitx

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

// CommitOp commits what is already staged in Dir.
type CommitOp struct {
	Dir     string   // worktree; callers stage first
	Message string   // full message including trailers
	Author  Identity // used as author and committer; never global config
}

// TokenFunc mints a push credential. It is called once per push.
type TokenFunc func(ctx context.Context) (string, error)

// PushOp pushes Ref to Remote.
type PushOp struct {
	Dir, Remote, Ref string    // Ref is a refspec such as "HEAD:refs/heads/main"
	ForceWithLease   string    // expected remote SHA of the destination ref; "" = normal push
	Token            TokenFunc // nil = no auth header (file remotes in tests)
}

// Commit commits the staged index and returns the new HEAD SHA. It returns
// ErrNothingToCommit when the index is identical to HEAD.
func (g *Git) Commit(ctx context.Context, op CommitOp) (string, error) {
	if op.Author.Name == "" || op.Author.Email == "" {
		return "", fmt.Errorf("gitx: commit in %s: author name and email are required", op.Dir)
	}
	if _, _, err := g.run(ctx, op.Dir, nil, "", nil, "diff", "--cached", "--quiet"); err == nil {
		return "", ErrNothingToCommit
	} else if !isExit(err, 1) {
		return "", fmt.Errorf("gitx: check index in %s: %w", op.Dir, err)
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + op.Author.Name, "GIT_AUTHOR_EMAIL=" + op.Author.Email,
		"GIT_COMMITTER_NAME=" + op.Author.Name, "GIT_COMMITTER_EMAIL=" + op.Author.Email,
	}
	// Hooks are disabled and signing is off so a commit never depends on the
	// host's git configuration.
	_, _, err := g.run(ctx, op.Dir, env, op.Message, nil,
		"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false",
		"commit", "--quiet", "-F", "-")
	if err != nil {
		return "", fmt.Errorf("gitx: commit in %s: %w", op.Dir, err)
	}
	return g.RevParse(ctx, op.Dir, "HEAD")
}

// Push pushes op.Ref. Typed errors: ErrNonFastForward, ErrStaleLease.
func (g *Git) Push(ctx context.Context, op PushOp) error {
	args := []string{"push", "--porcelain"}
	if op.ForceWithLease != "" {
		dst := op.Ref
		if i := strings.LastIndex(dst, ":"); i >= 0 {
			dst = dst[i+1:]
		}
		args = append(args, "--force-with-lease="+dst+":"+op.ForceWithLease)
	}
	args = append(args, op.Remote, op.Ref)
	var env, secrets []string
	if op.Token != nil {
		tok, err := op.Token(ctx)
		if err != nil {
			return fmt.Errorf("gitx: push token: %w", err)
		}
		env = pushEnv(tok)
		secrets = []string{tok}
	}
	stdout, stderr, err := g.run(ctx, op.Dir, env, "", secrets, args...)
	if err == nil {
		return nil
	}
	stderr = stdout + stderr // --porcelain reports rejections on stdout
	switch {
	case strings.Contains(stderr, "stale info"):
		return fmt.Errorf("gitx: push %s: %w", op.Ref, ErrStaleLease)
	case strings.Contains(stderr, "non-fast-forward"), strings.Contains(stderr, "fetch first"):
		return fmt.Errorf("gitx: push %s: %w", op.Ref, ErrNonFastForward)
	}
	return fmt.Errorf("gitx: push %s: %w", op.Ref, err)
}

// pushEnv passes the credential to git through the environment (never argv,
// which is visible in the process list).
func pushEnv(token string) []string {
	cred := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraheader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + cred,
	}
}

// scrub removes each secret, and its base64 forms, from s.
func scrub(s string, secrets ...string) string {
	for _, sec := range secrets {
		if sec == "" {
			continue
		}
		for _, form := range []string{
			base64.StdEncoding.EncodeToString([]byte("x-access-token:" + sec)),
			base64.StdEncoding.EncodeToString([]byte(sec)),
			sec,
		} {
			s = strings.ReplaceAll(s, form, "[REDACTED]")
		}
	}
	return s
}
