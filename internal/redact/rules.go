package redact

import (
	"bytes"
	"regexp"
)

// Every quantifier is bounded and at most 1000, so a match is always shorter
// than the scanner's window overlap.
type rule struct {
	id string
	re *regexp.Regexp
	// kw is a cheap prefilter: the rule runs only on lines containing one of
	// these. Empty means always run (the generic rule filters itself).
	kw      [][]byte
	generic bool
	// skip, when set, exempts an individual match from being a finding.
	skip func(match []byte) bool
}

func kws(s ...string) [][]byte {
	out := make([][]byte, len(s))
	for i, v := range s {
		out[i] = []byte(v)
	}
	return out
}

var rules = []rule{
	{id: "github-token", re: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,255}\b`), kw: kws("ghp_", "gho_", "ghu_", "ghs_", "ghr_")},
	{id: "github-fine-grained", re: regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{50,255}\b`), kw: kws("github_pat_")},
	{id: "aws-access-key", re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), kw: kws("AKIA", "ASIA"), skip: awsExample},
	{id: "slack-token", re: regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,250}`), kw: kws("xox")},
	{id: "slack-webhook", re: regexp.MustCompile(`hooks\.slack\.com/services/[A-Za-z0-9/]{20,200}`), kw: kws("hooks.slack.com")},
	{id: "private-key", re: regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`), kw: kws("PRIVATE KEY")},
	{id: "anthropic-key", re: regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,250}`), kw: kws("sk-ant-")},
	{id: "openai-key", re: regexp.MustCompile(`\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{20,250}`), kw: kws("sk-")},
	{id: "jwt", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,1000}\.eyJ[A-Za-z0-9_-]{10,1000}\.[A-Za-z0-9_-]{10,1000}`), kw: kws("eyJ")},
	{id: "gcp-api-key", re: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`), kw: kws("AIza")},
	{id: "stripe-live", re: regexp.MustCompile(`\b[rs]k_live_[A-Za-z0-9]{20,250}`), kw: kws("_live_")},
	// generic-assignment skips unquoted values that are a Go selector or
	// short identifier (u.Tokens) or all digits (token counts). Quoted values
	// are judged by entropy, so the rule stays fail-closed, except that any
	// value that is an upper-case snake-case environment variable name is
	// skipped.
	{id: "generic-assignment", generic: true,
		re: regexp.MustCompile(`(?i)(?:api[_-]?key|secret|token|passw(?:or)?d|auth)["']?\s{0,16}[:=]\s{0,16}(["']?)([A-Za-z0-9_\-+/=.]{16,250})`)},
}

// awsExample reports whether an aws-access-key match is one of AWS's published
// placeholder ids: 20 characters ending in the example suffix. It judges one
// match, never a line. The documented placeholder secret key needs no code:
// generic-assignment already skips values containing "example".
func awsExample(m []byte) bool {
	return len(m) == 20 && bytes.HasSuffix(m, []byte("EXA"+"MPLE"))
}

const entropyThreshold = 3.5

var placeholderRE = regexp.MustCompile(`(?i)example|placeholder|changeme|dummy|xxxx`)

// placeholder reports whether a generic-assignment value is an obvious
// non-secret.
func placeholder(v []byte) bool {
	if bytes.Contains(v, []byte("REDACTED")) {
		return true
	}
	if v[0] == '$' || v[0] == '<' || bytes.HasPrefix(v, []byte("{{")) {
		return true
	}
	if len(bytes.Trim(v, string(v[0]))) == 0 {
		return true
	}
	return envName(v) || placeholderRE.Match(v)
}

var envNameRE = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$`)

// envName reports whether v is an upper-case snake-case environment variable
// name: at least one underscore, no lower case, none of + / = . and at most 64
// bytes.
func envName(v []byte) bool {
	return len(v) <= 64 && envNameRE.Match(v)
}

var codeRefRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// codeRef reports whether v is an unquoted Go selector chain whose segments
// are each at most 24 bytes, or a bare identifier shorter than 32 bytes.
func codeRef(v []byte) bool {
	if !codeRefRE.Match(v) {
		return false
	}
	if bytes.IndexByte(v, '.') < 0 {
		return len(v) < 32
	}
	for _, seg := range bytes.Split(v, []byte(".")) {
		if len(seg) > 24 {
			return false
		}
	}
	return true
}

// numeric reports whether v is non-empty and all ASCII digits.
func numeric(v []byte) bool {
	if len(v) == 0 {
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
