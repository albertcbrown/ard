package gitx

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/albertcbrown/ard/internal/github"
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

// Push pushes op.Ref. Typed errors: ErrNonFastForward, ErrStaleLease,
// ErrProtected (GH006) and ErrTransient (the push may have landed).
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
			if ctx.Err() == nil && tokenTransient(err) {
				return fmt.Errorf("gitx: push token: %w: %v", ErrTransient, err)
			}
			return fmt.Errorf("gitx: push token: %w", err)
		}
		env = pushEnv(tok, op.Remote)
		secrets = []string{tok}
	}
	stdout, stderr, err := g.run(ctx, op.Dir, env, "", secrets, args...)
	if err == nil {
		return nil
	}
	stderr = stdout + stderr // --porcelain reports rejections on stdout
	if cerr := classifyPush(stderr); cerr != nil {
		if errors.Is(cerr, ErrTransient) {
			return fmt.Errorf("gitx: push %s: %w: %v", op.Ref, cerr, err)
		}
		return fmt.Errorf("gitx: push %s: %w", op.Ref, cerr)
	}
	return fmt.Errorf("gitx: push %s: %w", op.Ref, err)
}

// classifyPush maps git's push output to ErrStaleLease, ErrNonFastForward,
// ErrProtected or ErrTransient, or nil when it names none of them. The order
// matters: stale and non-fast-forward win over protected, which wins over
// transient.
func classifyPush(out string) error {
	switch {
	case strings.Contains(out, "stale info"):
		return ErrStaleLease
	case strings.Contains(out, "non-fast-forward"), strings.Contains(out, "fetch first"):
		return ErrNonFastForward
	case strings.Contains(out, "GH006"), strings.Contains(out, "Protected branch update failed"):
		return ErrProtected
	case classifyTransient(out):
		return ErrTransient
	}
	return nil
}

// transientMarkers are git messages (matched case-insensitively) that name a
// failure likely to pass on its own.
var transientMarkers = []string{
	"remote: internal server error",
	"returned error: 500", "returned error: 502", "returned error: 503", "returned error: 504",
	"could not resolve host",
	"connection reset",
	"operation timed out",
	"connection timed out",
	"failed to connect",
	"ssl_read",
	"early eof",
	"rpc failed",
}

// classifyTransient reports whether git's stderr names a transient failure.
func classifyTransient(stderr string) bool {
	s := strings.ToLower(stderr)
	for _, m := range transientMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// tokenTransient reports whether a token mint or refresh error comes from the
// network or a 5xx, not from a refusal.
func tokenTransient(err error) bool { return github.IsTransientToken(err) }

// pushEnv passes the credential to git through the environment (never argv,
// which is visible in the process list).
func pushEnv(token, remote string) []string {
	cred := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=" + headerKey(remote),
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

// headerKey scopes the auth header to the remote's scheme and host, so a
// redirect to another host never receives the credential.
func headerKey(remote string) string {
	u, err := url.Parse(remote)
	if err != nil || u.Host == "" {
		return "http.extraheader"
	}
	return "http." + u.Scheme + "://" + u.Host + "/.extraheader"
}
