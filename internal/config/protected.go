package config

import (
	"fmt"
	"path"
	"strings"
	"unicode"

	"github.com/bmatcuk/doublestar/v4"
)

// normaliseProtectedPath validates one merge.protected_paths entry and
// returns its canonical form. An entry ending in "/" is a literal directory
// prefix and keeps exactly one trailing "/"; any other entry is a doublestar
// glob, path-cleaned.
func normaliseProtectedPath(p string) (string, error) {
	if strings.TrimSpace(p) != p || strings.IndexFunc(p, badRune) >= 0 {
		return "", fmt.Errorf("path %q has leading/trailing whitespace or control characters", p)
	}
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
// Matching is case-insensitive (macOS file systems are, so `Claude.md` is `CLAUDE.md`) and anchored at the repo root. An entry ending
// in "/" matches that directory (and the bare name, in case a file or
// submodule replaces it) on a directory boundary; any other entry is a
// doublestar glob where "*" stays within one segment and "**" crosses them.
func (r *Resolved) ProtectedBy(p string) (pattern string, ok bool) {
	if p == "" || strings.HasPrefix(p, "/") {
		return "", true
	}
	// GitHub only reports canonical paths; anything else is malformed.
	if path.Clean(p) != p || strings.Contains(p, "\\") || strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return "", true
	}
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return "", true
	}
	for _, e := range r.ProtectedPaths {
		if matchProtected(e, p) {
			return e, true
		}
	}
	return "", false
}

// matchProtected matches one canonical entry against a cleaned path.
func matchProtected(entry, q string) bool {
	entry, q = strings.ToLower(entry), strings.ToLower(q)
	if strings.HasSuffix(entry, "/") {
		return strings.HasPrefix(q, entry) || q == strings.TrimSuffix(entry, "/")
	}
	m, err := doublestar.Match(entry, q)
	return err != nil || m // an invalid pattern fails closed
}

// badRune reports control and format characters (such as zero-width
// spaces), which make an entry look right but match nothing.
func badRune(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }
