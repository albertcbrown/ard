package committer

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const maxSummaryRunes = 72

var (
	skipRe       = regexp.MustCompile(`(?i)\[(skip ci|ci skip|no ci|skip actions|actions skip)\]`)
	skipChecksRe = regexp.MustCompile(`(?i)skip-checks\s*:`)
)

// Message builds the commit message: ard(<stage>:<role>): #<issue> <summary>
// with " [skip ci]" when skipCI, and an Ard-Job trailer when jobID is set.
// The summary is untrusted: control characters and newlines become spaces,
// so it cannot forge a trailer or a [skip ci] marker line, closing
// references ("Fixes #12") are neutralised by NeutraliseRefs, and it is cut to
// 72 runes.
func Message(stage, role string, issue int, summary, jobID string, skipCI bool) (string, error) {
	s := strings.Join(strings.FieldsFunc(summary, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	}), " ")
	// GitHub honours these tokens anywhere in a message, so an untrusted
	// summary must not be able to skip CI.
	s = skipRe.ReplaceAllString(s, "(skip-ci)")
	s = skipChecksRe.ReplaceAllString(s, "skip-checks -")
	// A closing keyword before an issue reference would close it when the
	// commit reaches main. Done before the cut so truncation only shortens
	// already-safe text.
	s = NeutraliseRefs(s)
	if s == "" {
		return "", fmt.Errorf("%w: empty summary", ErrInvalid)
	}
	if r := []rune(s); len(r) > maxSummaryRunes {
		s = strings.TrimSpace(string(r[:maxSummaryRunes-1])) + "…"
	}
	msg := fmt.Sprintf("ard(%s:%s): #%d %s", stage, role, issue, s)
	if skipCI {
		msg += " [skip ci]"
	}
	if jobID != "" {
		msg += "\n\nArd-Job: " + jobID
	}
	return msg, nil
}
