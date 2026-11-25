// Package ui is the read-only terminal UI of `ard serve` (the command
// center). It draws a snapshot.Snapshot; it never calls GitHub.
package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Clean makes an untrusted string safe to draw: escape sequences (CSI, OSC,
// DCS and the like) are dropped whole, other C0/C1 controls, DEL, bidi
// overrides and invalid UTF-8 are removed, and whitespace controls become one
// space.
func Clean(s string) string {
	// Drop whole escape sequences first, so "\x1b[31m" leaves no "[31m".
	s = stripSequences(s)
	var b strings.Builder
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		s = s[n:]
		switch {
		case r == utf8.RuneError && n <= 1:
			continue
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			continue
		case isBidi(r):
			continue
		case !unicode.IsPrint(r) && r != ' ':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isBidi(r rune) bool {
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x200e || r == 0x200f || r == 0x061c
}

// stripSequences removes ESC and C1-introduced sequences using the ansi
// parser. A lone C1 introducer (U+009B and friends) is handled by Clean.
func stripSequences(s string) string {
	// ansi.Strip handles 7-bit ESC sequences. The 8-bit C1 forms are valid
	// UTF-8 only as U+0080..U+009F runes; Clean removes those runes, and the
	// text after them is plain text.
	return ansi.Strip(s)
}

// Trunc cleans s and cuts it to at most width display cells, ending with an
// ellipsis when it was cut.
func Trunc(s string, width int) string {
	s = Clean(s)
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}
