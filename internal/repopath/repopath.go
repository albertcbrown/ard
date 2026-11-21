// Package repopath maps a GitHub "owner/name" to local path segments.
// GitHub names are case-insensitive, so both parts are lowercased. The
// segments are nested (<owner>/<repo>) so that distinct repos never share a
// directory: "a-b/c" and "a/b-c" would collide if joined with a dash.
package repopath

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrInvalid means the repo name cannot be used as a path.
var ErrInvalid = errors.New("invalid repo")

var partRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Parts validates repo ("owner/name") and returns both parts lowercased.
// The charset is ASCII only, so lowercasing is plain ASCII folding.
func Parts(repo string) (owner, name string, err error) {
	bad := func(reason string) (string, string, error) {
		return "", "", fmt.Errorf("%w %q: %s", ErrInvalid, repo, reason)
	}
	if strings.Count(repo, "/") != 1 {
		return bad("must be owner/name")
	}
	o, n, _ := strings.Cut(repo, "/")
	for _, p := range []string{o, n} {
		switch {
		case p == "":
			return bad("empty part")
		case p == "." || p == "..":
			return bad("part must not be . or ..")
		case !partRE.MatchString(p):
			return bad("part has characters outside [A-Za-z0-9._-]")
		}
	}
	return strings.ToLower(o), strings.ToLower(n), nil
}

// Join returns filepath.Join(base, owner, name) for the validated, lowercased parts.
func Join(base, repo string) (string, error) {
	o, n, err := Parts(repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, o, n), nil
}
