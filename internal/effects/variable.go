package effects

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/repopath"
)

// ErrNoOperatorToken is returned by SetRepoVariable when the operator's token
// is not available. The App cannot write Actions variables.
var ErrNoOperatorToken = errors.New("effects: no operator token to write a repository variable; " +
	"set ARD_GITHUB_TOKEN to a personal access token or run: gh auth login")

var varNameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// SetRepoVariable creates or updates an Actions variable of repo
// (owner/name) with the operator's token. The token is read once per call,
// held in memory, and never logged.
func (l *Live) SetRepoVariable(ctx context.Context, repo, name, value string) error {
	owner, rname, err := repopath.Parts(repo)
	if err != nil {
		return l.done(ctx, "SetRepoVariable", 0, err, "repo", repo)
	}
	if !varNameRE.MatchString(name) {
		return l.done(ctx, "SetRepoVariable", 0, fmt.Errorf("invalid variable name %q", name), "repo", repo)
	}
	if l.operatorToken == nil {
		return l.done(ctx, "SetRepoVariable", 0, ErrNoOperatorToken, "repo", repo, "name", name)
	}
	tok, err := l.operatorToken(ctx)
	if err != nil || tok == "" {
		return l.done(ctx, "SetRepoVariable", 0, ErrNoOperatorToken, "repo", repo, "name", name)
	}
	cfg := l.ghCfg
	cfg.Repo = github.Repo{Owner: owner, Name: rname}
	cfg.Auth = github.NewTokenAuth(tok)
	cfg.ReadOnly = false
	c, err := github.New(cfg)
	if err != nil {
		return l.done(ctx, "SetRepoVariable", 0, err, "repo", repo, "name", name)
	}
	return l.done(ctx, "SetRepoVariable", 0, c.SetVariable(ctx, name, value), "repo", repo, "name", name, "value_len", len(value))
}

// SetRepoVariable records the call and writes nothing.
func (d *DryRun) SetRepoVariable(ctx context.Context, repo, name, value string) error {
	_, err := d.emit(ctx, "SetRepoVariable", repo, map[string]any{"name": name, "value": value}, false, nil)
	return err
}
