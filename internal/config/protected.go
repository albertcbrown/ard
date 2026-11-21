package config

import (
	"fmt"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// normaliseProtectedPath validates one merge.protected_paths entry and
// returns its canonical form. An entry ending in "/" is a literal directory
// prefix and keeps exactly one trailing "/"; any other entry is a doublestar
// glob, path-cleaned.
func normaliseProtectedPath(p string) (string, error) {
	prefix := strings.HasSuffix(p, "/")
	clean, err := normaliseRelPath(p)
	if err != nil {
		return "", err
	}
	if clean == "." {
		return "", fmt.Errorf("path %q names the whole repo; use %q to protect everything", p, "**")
	}
	if prefix {
		if strings.ContainsAny(clean, "*?[{\\") {
			return "", fmt.Errorf("prefix %q contains glob characters; write it as a glob such as %q", p, clean+"/**")
		}
		return clean + "/", nil
	}
	if !doublestar.ValidatePattern(clean) {
		return "", fmt.Errorf("invalid glob %q", p)
	}
	return clean, nil
}

// Protected reports whether the repo-relative, slash-separated path p is
// covered by merge.protected_paths. The merger must call it for every
// changed file's path and, for a rename, its previous path too.
func (r *Resolved) Protected(p string) bool {
	_, ok := r.ProtectedBy(p)
	return ok
}

// ProtectedBy is Protected plus the entry that matched (first in config
// order). A malformed p (empty, absolute, or escaping the repo after
// path.Clean) is reported as protected with pattern "": the check fails
// closed.
//
// Matching is case-sensitive and anchored at the repo root. An entry ending
// in "/" matches that directory (and the bare name, in case a file or
// submodule replaces it) on a directory boundary; any other entry is a
// doublestar glob where "*" stays within one segment and "**" crosses them.
func (r *Resolved) ProtectedBy(p string) (pattern string, ok bool) {
	if p == "" || strings.HasPrefix(p, "/") {
		return "", true
	}
	q := path.Clean(p)
	if q == "." || q == ".." || strings.HasPrefix(q, "../") {
		return "", true
	}
	for _, e := range r.ProtectedPaths {
		if matchProtected(e, q) {
			return e, true
		}
	}
	return "", false
}

// matchProtected matches one canonical entry against a cleaned path.
func matchProtected(entry, q string) bool {
	if strings.HasSuffix(entry, "/") {
		return strings.HasPrefix(q, entry) || q == strings.TrimSuffix(entry, "/")
	}
	m, err := doublestar.Match(entry, q)
	return err != nil || m // an invalid pattern fails closed
}
