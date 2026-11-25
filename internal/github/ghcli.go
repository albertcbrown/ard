package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// GhLoginCommand is what a user runs when `gh` is installed but logged out.
const GhLoginCommand = "gh auth login --hostname github.com --git-protocol https --scopes repo"

// Errors from GhFallback.
var (
	ErrNoGh = errors.New("no GitHub credentials: set the GitHub App variables " +
		"(ARD_GITHUB_APP_ID, ARD_GITHUB_INSTALLATION_ID, ARD_GITHUB_PRIVATE_KEY or ARD_GITHUB_PRIVATE_KEY_FILE) " +
		"or ARD_GITHUB_TOKEN, or install the gh CLI and log in; gh is not installed")
	ErrGhNotLoggedIn = errors.New("no GitHub credentials: gh is installed but not logged in; run: " + GhLoginCommand)
)

// RunFunc runs a command and returns its stdout. Stderr is dropped: it may
// echo a token.
type RunFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRun is the RunFunc backed by os/exec.
func ExecRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// GhFallback reads the gh CLI's token for this session. lookPath and run are
// seams for tests. The token is returned in memory only; errors never carry
// it or gh's stderr.
func GhFallback(ctx context.Context, lookPath func(string) (string, error), run RunFunc, out io.Writer) (*TokenAuth, error) {
	gh, err := lookPath("gh")
	if err != nil {
		return nil, ErrNoGh
	}
	if _, err := run(ctx, gh, "auth", "status", "--hostname", "github.com"); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrGhNotLoggedIn
	}
	fmt.Fprintln(out, "ard: no GitHub credentials set; using the gh token for this session (running: gh auth token --hostname github.com). It is kept in memory only.")
	b, err := run(ctx, gh, "auth", "token", "--hostname", "github.com")
	if err != nil {
		return nil, errors.New("github: gh auth token failed")
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return nil, errors.New("github: gh auth token printed no token")
	}
	return &TokenAuth{Value: tok, Source: "gh"}, nil
}
