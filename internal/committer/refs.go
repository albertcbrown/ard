package committer

import (
	"regexp"
	"strings"
)

const (
	refHash  = `#\d+`
	refGH    = `GH-\d+`
	refCross = `[\w.-]+/[\w.-]+#\d+`
	refURL   = `https?://[^\s/]+/[\w.-]+/[\w.-]+/(?:issues|pull)/\d+`
	// refAny tries the longest forms first so "o/r#12" is not read as "#12".
	refAny = `(?:` + refURL + `|` + refCross + `|` + refGH + `|` + refHash + `)`
)

var (
	// A closing keyword directly before a reference, then any further
	// references in a list ("#1, #2 and #3"). GitHub closes only when the
	// keyword comes right before the reference, so words in between stop the
	// match. The keyword needs no leading word boundary and the separator is
	// permissive, so ambiguous forms are rewritten: the safe direction.
	closingRe = regexp.MustCompile(`(?i)(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)[\s:*_~\x60(\[<]*` +
		refAny + `(?:\s*(?:,|and|&)\s*` + refAny + `)*`)
	refRe      = regexp.MustCompile(`(?i)` + refAny)
	urlRefRe   = regexp.MustCompile(`(?i)^https?://[^\s/]+/([\w.-]+/[\w.-]+)/(?:issues|pull)/(\d+)$`)
	crossRefRe = regexp.MustCompile(`^([\w.-]+/[\w.-]+)#(\d+)$`)
)

// NeutraliseRefs rewrites every issue or PR reference that sits directly
// after a GitHub closing keyword (close, fix, resolve and their forms), so the
// text can go in a commit message, PR title or PR body without closing
// anything: "Fixes #12" becomes "Fixes issue 12". It handles #N, owner/repo#N,
// GH-N and issue or PR URLs, with optional ":" or markdown emphasis between
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
		for _, ix := range refRe.FindAllStringIndex(m, -1) {
			b.WriteString(m[last:ix[0]])
			// "fixes#12" has no separator; keep the words apart.
			if ix[0] > 0 && !strings.ContainsAny(m[ix[0]-1:ix[0]], " \t\r\n:*_~`([<,&") {
				b.WriteByte(' ')
			}
			b.WriteString(plainRef(m[ix[0]:ix[1]]))
			last = ix[1]
		}
		return b.String() + m[last:]
	})
}

func plainRef(r string) string {
	if sm := urlRefRe.FindStringSubmatch(r); sm != nil {
		return sm[1] + " issue " + sm[2]
	}
	if sm := crossRefRe.FindStringSubmatch(r); sm != nil {
		return sm[1] + " issue " + sm[2]
	}
	return "issue " + r[strings.IndexAny(r, "0123456789"):]
}
