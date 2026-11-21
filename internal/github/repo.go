package github

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseRepo parses "owner/name".
func ParseRepo(s string) (Repo, error) {
	parts := strings.Split(strings.TrimSpace(s), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Repo{}, fmt.Errorf("github: invalid repo %q, want owner/name", s)
	}
	return Repo{Owner: parts[0], Name: parts[1]}, nil
}

// RepoFromRemote extracts the repo from a git remote URL: https://github.com/o/r(.git),
// git@github.com:o/r(.git) or ssh://git@github.com/o/r(.git).
func RepoFromRemote(remote string) (Repo, error) {
	remote = strings.TrimSpace(remote)
	var path string
	switch {
	case strings.Contains(remote, "://"):
		u, err := url.Parse(remote)
		if err != nil {
			return Repo{}, fmt.Errorf("github: parse remote %q: %w", remote, err)
		}
		if !strings.EqualFold(u.Hostname(), "github.com") {
			return Repo{}, fmt.Errorf("github: remote %q is not on github.com", remote)
		}
		path = u.Path
	case strings.HasPrefix(remote, "git@"):
		host, p, ok := strings.Cut(strings.TrimPrefix(remote, "git@"), ":")
		if !ok || !strings.EqualFold(host, "github.com") {
			return Repo{}, fmt.Errorf("github: remote %q is not on github.com", remote)
		}
		path = p
	default:
		return Repo{}, fmt.Errorf("github: unrecognised remote %q", remote)
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	r, err := ParseRepo(path)
	if err != nil {
		return Repo{}, fmt.Errorf("github: remote %q: %w", remote, err)
	}
	return r, nil
}
