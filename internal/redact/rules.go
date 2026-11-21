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
	{id: "aws-access-key", re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), kw: kws("AKIA", "ASIA")},
	{id: "slack-token", re: regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,250}`), kw: kws("xox")},
	{id: "slack-webhook", re: regexp.MustCompile(`hooks\.slack\.com/services/[A-Za-z0-9/]{20,200}`), kw: kws("hooks.slack.com")},
	{id: "private-key", re: regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`), kw: kws("PRIVATE KEY")},
	{id: "anthropic-key", re: regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,250}`), kw: kws("sk-ant-")},
	{id: "openai-key", re: regexp.MustCompile(`\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{20,250}`), kw: kws("sk-")},
	{id: "jwt", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,1000}\.eyJ[A-Za-z0-9_-]{10,1000}\.[A-Za-z0-9_-]{10,1000}`), kw: kws("eyJ")},
	{id: "gcp-api-key", re: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`), kw: kws("AIza")},
	{id: "stripe-live", re: regexp.MustCompile(`\b[rs]k_live_[A-Za-z0-9]{20,250}`), kw: kws("_live_")},
	{id: "generic-assignment", generic: true,
		re: regexp.MustCompile(`(?i)(?:api[_-]?key|secret|token|passw(?:or)?d|auth)["']?\s*[:=]\s*["']?([A-Za-z0-9_\-+/=.]{16,250})`)},
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
	return placeholderRE.Match(v)
}
