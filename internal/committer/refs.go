package committer

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// hashMark is "#" in its plain, full-width, HTML-entity and
	// backslash-escaped forms; GitHub renders each of them as "#" in markdown.
	hashMark = `(?:[#\x{FF03}]|&#0*35;|&#[xX]0*23;|&num;|\\#)`
	// digit is a decimal digit, possibly entity-encoded (&#49; or &#x31;).
	digit    = `(?:\d|&#0*(?:4[89]|5[0-7]);|&#[xX]0*3[0-9];)`
	refHash  = hashMark + digit + `+`
	refGH    = `GH-` + digit + `+`
	refCross = `[\w.-]+/[\w.-]+` + hashMark + digit + `+`
	refURL   = `(?:https?://)?[\w.-]+(?::\d+)?/[\w.-]+/[\w.-]+/(?:issues|pull)/` + digit + `+`
	// refAny tries the longest forms first so "o/r#12" is not read as "#12".
	refAny = `(?:` + refURL + `|` + refCross + `|` + refGH + `|` + refHash + `)`
)

var (
	// A closing keyword directly before a reference, then any further
	// references in a list ("#1, #2 and #3"). GitHub closes only when the
	// keyword comes right before the reference, so words in between stop the
	// match. The keyword needs no leading word boundary and the separator is
	// permissive, so ambiguous forms are rewritten: the safe direction.
	closingRe = regexp.MustCompile(`(?i)(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)(?:[\s\p{Z}\p{Cf}:*_~\x60(\[<]|&nbsp;|&#0*32;|&#0*160;|&#[xX]0*(?:20|[aA]0);)*` +
		// Optional markdown link text: "Fixes [text](url)".
		`(?:[^\]\[\n]*\]\()?` +
		refAny + `(?:[\s\p{Z}\p{Cf}]*(?:,|and|&)[\s\p{Z}\p{Cf}]*` + refAny + `)*`)
	refRe       = regexp.MustCompile(`(?i)` + refAny)
	urlRefRe    = regexp.MustCompile(`(?i)^(?:https?://)?[\w.-]+(?::\d+)?/([\w.-]+/[\w.-]+)/(?:issues|pull)/(\d+)$`)
	crossRefRe  = regexp.MustCompile(`^([\w.-]+/[\w.-]+)` + hashMark + `(\d+)$`)
	trailingNum = regexp.MustCompile(`\d+$`)
)

// NeutraliseRefs rewrites every issue or PR reference that sits directly
// after a GitHub closing keyword (close, fix, resolve and their forms), so the
// text can go in a commit message, PR title or PR body without closing
// anything: "Fixes #12" becomes "Fixes issue 12". It handles #N, owner/repo#N,
// GH-N and issue or PR URLs (the "#" may also be written as an HTML entity,
// &#35; &#x23; &num;, or backslash-escaped, \#, which markdown renders as "#"), with optional ":" or markdown emphasis between
// keyword and reference, and reference lists. References with no keyword
// directly before them are left alone, as GitHub does not close on them.
// It is idempotent.
//
// Every caller that puts untrusted text into a commit message, PR title or PR
// body must use it. ard's own "Closes #<issue>" line is written by the caller
// after neutralising and is never passed through it.
func NeutraliseRefs(s string) string {
	return closingRe.ReplaceAllStringFunc(s, func(m string) string {
		var b strings.Builder
		last := 0
		seps := sepEntityRe.FindAllStringIndex(m, -1)
	refs:
		for _, ix := range refRe.FindAllStringIndex(m, -1) {
			// "&#32;" is a separator entity, not the reference "#32".
			for _, sp := range seps {
				if ix[0] >= sp[0] && ix[0] < sp[1] {
					continue refs
				}
			}
			b.WriteString(m[last:ix[0]])
			// "fixes#12" has no separator; keep the words apart.
			if prev, _ := utf8.DecodeLastRuneInString(m[:ix[0]]); ix[0] > 0 && (unicode.IsLetter(prev) || unicode.IsDigit(prev)) {
				b.WriteByte(' ')
			}
			b.WriteString(plainRef(m[ix[0]:ix[1]]))
			last = ix[1]
		}
		return b.String() + m[last:]
	})
}

var sepEntityRe = regexp.MustCompile(`&nbsp;|&#0*32;|&#0*160;|&#[xX]0*(?:20|[aA]0);`)

var digitEntityRe = regexp.MustCompile(`&#0*(?:4[89]|5[0-7]);|&#[xX]0*3[0-9];`)

// decodeDigits turns entity-encoded digits into plain ones.
func decodeDigits(s string) string {
	return digitEntityRe.ReplaceAllStringFunc(s, func(m string) string {
		v := strings.TrimSuffix(strings.TrimPrefix(m, "&#"), ";")
		base := 10
		if v[0] == 'x' || v[0] == 'X' {
			v, base = v[1:], 16
		}
		n, _ := strconv.ParseInt(v, base, 32)
		return string(rune(n))
	})
}

func plainRef(r string) string {
	r = decodeDigits(r)
	if sm := urlRefRe.FindStringSubmatch(r); sm != nil {
		return sm[1] + " issue " + sm[2]
	}
	if sm := crossRefRe.FindStringSubmatch(r); sm != nil {
		return sm[1] + " issue " + sm[2]
	}
	return "issue " + trailingNum.FindString(r)
}
