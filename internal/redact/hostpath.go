package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

// ErrBadPathRule is returned by WithPaths for an unusable rule.
var ErrBadPathRule = errors.New("redact: bad path rule")

// PathRule replaces an absolute host path with a fixed placeholder. A match
// must end at a path boundary: end of input or a byte outside [A-Za-z0-9_-]
// (non-ASCII bytes are boundaries, so `/Users/alé` becomes `~é`: over-redaction
// is the safe direction, and prose is full of Unicode punctuation).
type PathRule struct {
	Name string // [A-Za-z0-9_.-]{1,64}; the LogResult.Replaced key, e.g. "path.home"
	From string // absolute; trailing "/" trimmed; not "/" after trimming
	To   string // non-empty placeholder, written as is
}

// WithPaths returns a new set holding s's values (s may be nil) and the path
// rules. s is not changed. The first bad rule fails the call. A From that is
// already present keeps the first rule, so callers order rules most specific
// first. The error never contains From: it holds the operator's username.
func (s *Secrets) WithPaths(rules ...PathRule) (*Secrets, error) {
	n := NewSecrets()
	if s != nil {
		n.entries = append(n.entries, s.entries...)
	}
	have := map[string]bool{}
	for _, e := range n.entries {
		if e.path {
			have[e.value] = true
		}
	}
	for _, r := range rules {
		from := strings.TrimRight(r.From, "/")
		if !nameRE.MatchString(r.Name) || r.To == "" || from == "" || !path.IsAbs(from) ||
			strings.ContainsAny(from, "\x00\n") {
			return nil, fmt.Errorf("path rule %q: %w", r.Name, ErrBadPathRule)
		}
		if have[from] {
			continue
		}
		have[from] = true
		n.entries = append(n.entries, entry{name: r.Name, value: from, path: true, to: r.To})
	}
	n.rebuild()
	return n, nil
}

// pathVariantsOf returns the raw path, its JSON string-escaped inner form when
// that differs, and the \/-escaped form.
func pathVariantsOf(p string) []string {
	out := []string{p}
	var jb bytes.Buffer
	enc := json.NewEncoder(&jb)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err == nil {
		if j := bytes.TrimSuffix(jb.Bytes(), []byte("\n")); len(j) >= 2 {
			out = append(out, string(j[1:len(j)-1]))
		}
	}
	return append(out, strings.ReplaceAll(p, "/", `\/`))
}

// isNameByte reports whether c continues a path component name. Non-ASCII
// bytes do not: an em-dash or curly quote after a path must not hide it.
func isNameByte(c byte) bool {
	return c == '_' || c == '-' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func (v *variant) replacement() []byte {
	if v.repl != nil {
		return v.repl
	}
	return []byte(marker(v.name))
}
