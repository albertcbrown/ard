package redact

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// MinSecretLen is the shortest value that is treated as a known secret.
// Shorter values are skipped and logged by name, never by value.
const MinSecretLen = 8

// ErrBadName is returned for a secret name that is not [A-Za-z0-9_.-]{1,64}.
var ErrBadName = errors.New("redact: bad secret name")

var (
	nameRE    = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	environRE = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|PASSWD|PRIVATE_KEY|API_KEY|CREDENTIAL|AUTH)`)
)

type variant struct {
	text []byte
	name string
}

type entry struct{ name, value string }

// Secrets is the per-job set of known values. Build it once, then treat it as
// read-only; it is safe for concurrent reads after the last Add.
type Secrets struct {
	entries  []entry
	variants []variant // longest first
	byFirst  [256][]int
	maxLen   int
}

// NewSecrets returns an empty set.
func NewSecrets() *Secrets { return &Secrets{} }

// Add registers a known secret value under name. Values shorter than
// MinSecretLen are skipped with a warning that names the key, not the value.
func (s *Secrets) Add(name, value string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("add secret %q: %w", name, ErrBadName)
	}
	if len(value) < MinSecretLen {
		slog.Warn("redact: secret value too short, not redacted", "name", name, "min_len", MinSecretLen)
		return nil
	}
	s.entries = append(s.entries, entry{name, value})
	s.rebuild()
	return nil
}

// AddEnv adds every value in env, using the key as the name.
func (s *Secrets) AddEnv(env map[string]string) error {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := s.Add(k, env[k]); err != nil {
			return err
		}
	}
	return nil
}

// FromEnviron adds values from a KEY=VALUE list (os.Environ()) whose key looks
// like a credential. Keys ending _FILE, _PATH or _DIR are skipped: they are
// paths, not secrets.
func FromEnviron(environ []string) *Secrets {
	s := NewSecrets()
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !environRE.MatchString(k) {
			continue
		}
		u := strings.ToUpper(k)
		if strings.HasSuffix(u, "_FILE") || strings.HasSuffix(u, "_PATH") || strings.HasSuffix(u, "_DIR") {
			continue
		}
		_ = s.Add(k, v) // bad names are skipped
	}
	return s
}

// Merge returns a new set holding the values of s then o. Either may be nil.
func (s *Secrets) Merge(o *Secrets) *Secrets {
	n := NewSecrets()
	for _, src := range []*Secrets{s, o} {
		if src != nil {
			n.entries = append(n.entries, src.entries...)
		}
	}
	n.rebuild()
	return n
}

func (s *Secrets) rebuild() {
	seen := map[string]bool{}
	s.variants = s.variants[:0]
	for _, e := range s.entries {
		for _, v := range variantsOf(e.value) {
			if seen[v] {
				continue
			}
			seen[v] = true
			s.variants = append(s.variants, variant{[]byte(v), e.name})
		}
	}
	sort.SliceStable(s.variants, func(i, j int) bool { return len(s.variants[i].text) > len(s.variants[j].text) })
	s.byFirst = [256][]int{}
	s.maxLen = 0
	for i, v := range s.variants {
		s.byFirst[v.text[0]] = append(s.byFirst[v.text[0]], i) // already longest first
		if len(v.text) > s.maxLen {
			s.maxLen = len(v.text)
		}
	}
}

func variantsOf(v string) []string {
	out := []string{v}
	b := []byte(v)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		out = append(out, enc.EncodeToString(b))
	}
	out = append(out, url.QueryEscape(v), url.PathEscape(v))
	// JSON-escaped forms: encoding/json escapes <, > and & by default, while
	// JavaScript's JSON.stringify (stream-json logs) does not. Keep both.
	if j, err := json.Marshal(v); err == nil && len(j) >= 2 {
		out = append(out, string(j[1:len(j)-1]))
	}
	var jb bytes.Buffer
	enc := json.NewEncoder(&jb)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err == nil {
		if j := bytes.TrimSuffix(jb.Bytes(), []byte("\n")); len(j) >= 2 {
			out = append(out, string(j[1:len(j)-1]))
		}
	}
	if strings.Contains(v, "\n") {
		for _, l := range strings.Split(v, "\n") {
			l = strings.TrimRight(l, "\r")
			if len(l) >= MinSecretLen {
				out = append(out, l)
			}
		}
	}
	return out
}

func marker(name string) string { return "[REDACTED:" + name + "]" }

// matchAt returns the longest variant starting at b[i], or nil.
func (s *Secrets) matchAt(b []byte, i int) *variant {
	for _, idx := range s.byFirst[b[i]] {
		v := &s.variants[idx]
		if len(b)-i >= len(v.text) && string(b[i:i+len(v.text)]) == string(v.text) {
			return v
		}
	}
	return nil
}
