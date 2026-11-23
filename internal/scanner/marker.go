package scanner

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/albertcbrown/ard/internal/costblock"
)

// Issue body layout. Everything the controller reads back sits between the
// findings markers; text outside the spans belongs to humans and survives
// every rewrite.
const (
	findingsOpen  = "<!-- ard:findings -->"
	findingsClose = "<!-- /ard:findings -->"
	detailsOpen   = "<!-- ard:details -->"
	detailsClose  = "<!-- /ard:details -->"

	// MaxBodyChars is the size the controller keeps an issue body under;
	// GitHub's own limit is 65,536.
	MaxBodyChars = 60000
	// MaxDetailChars caps one finding's details in the issue body; issue.md
	// holds them untruncated.
	MaxDetailChars = 2000
	// MaxTitleChars caps an issue title.
	MaxTitleChars = 256
)

// Line is one checklist line of an issue body: one finding.
type Line struct {
	Checked      bool
	Title        string // text of the line, including a "(regression of #N)" suffix
	Fingerprint  string
	Severity     Severity
	RegressionOf int // 0 = none
}

// Parsed is what ParseBody reads from an issue body.
type Parsed struct {
	Scanner string
	Group   string
	Lines   []Line
	HasSpan bool // the findings span is present
}

const encSafe = "._:/@+=,~-"

func encSafeByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(encSafe, c) >= 0
}

// EncodeField percent-encodes every byte outside [A-Za-z0-9._:/@+=,~-] as
// uppercase %XX, so an encoded value can never close an HTML comment or split
// the marker's fields.
func EncodeField(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if c := s[i]; encSafeByte(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// DecodeField is the strict inverse of EncodeField: a malformed or lowercase
// escape, or any byte EncodeField would have escaped, is an error.
func DecodeField(s string) (string, bool) {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '%':
			if i+2 >= len(s) {
				return "", false
			}
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if !ok1 || !ok2 {
				return "", false
			}
			out = append(out, hi<<4|lo)
			i += 2
		case encSafeByte(c):
			out = append(out, c)
		default:
			return "", false
		}
	}
	return string(out), true
}

// neutralize makes untrusted text inert inside an issue body: comment
// delimiters are escaped so scanner output cannot forge a marker or close a
// span. Newlines survive only when multiline is true.
func neutralize(s string, multiline bool) string {
	if !multiline {
		s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
	}
	s = strings.ReplaceAll(s, "<!--", "&lt;!--")
	s = strings.ReplaceAll(s, "-->", "--&gt;")
	return strings.ReplaceAll(s, "--!>", "--!&gt;")
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// defuseMentions replaces an @ that starts a word and is followed by a word
// character with a fullwidth commercial at, which notifies nobody. An @ inside
// an address such as x@y.com stays as it is.
func defuseMentions(s string) string {
	r := []rune(s)
	var b strings.Builder
	for i, c := range r {
		if c == '@' && i+1 < len(r) && isWordRune(r[i+1]) && (i == 0 || !isWordRune(r[i-1])) {
			c = '＠'
		}
		b.WriteRune(c)
	}
	return b.String()
}

// SafeTitle makes one line of untrusted text inert in an issue title or
// checklist line: control characters collapse to one space, backticks become
// apostrophes, angle brackets and link brackets are swapped for lookalikes and
// mentions are defused. It is idempotent.
func SafeTitle(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.NewReplacer("`", "'", "<", "‹", ">", "›", "[", "(", "]", ")").Replace(s)
	return strings.Join(strings.Fields(defuseMentions(s)), " ")
}

// InlineCode wraps untrusted text in an inline code span that it cannot leave.
func InlineCode(s string) string {
	return "`" + strings.ReplaceAll(neutralize(s, false), "`", "'") + "`"
}

// neutralizeBody defuses mentions and raw HTML tags in a scanner's free text
// outside fenced code blocks and inline code spans.
func neutralizeBody(s string) string {
	lines := strings.Split(s, "\n")
	fenced := false
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimLeft(ln, " "), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		parts := strings.Split(ln, "`")
		for j := 0; j < len(parts); j++ {
			// Odd parts sit inside a span, unless the span never closes.
			if j%2 == 1 && j < len(parts)-1 {
				continue
			}
			parts[j] = defuseTags(defuseMentions(parts[j]))
		}
		lines[i] = strings.Join(parts, "`")
	}
	return strings.Join(lines, "\n")
}

// defuseTags escapes a < that would begin an HTML tag, comment or declaration.
func defuseTags(s string) string {
	r := []rune(s)
	var b strings.Builder
	for i, c := range r {
		if c == '<' && i+1 < len(r) && (unicode.IsLetter(r[i+1]) || strings.ContainsRune("/!?", r[i+1])) {
			b.WriteString("&lt;")
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

func lineText(l Line) string {
	t := SafeTitle(l.Title)
	if l.RegressionOf > 0 && !strings.Contains(t, "(regression of #") {
		t += fmt.Sprintf(" (regression of #%d)", l.RegressionOf)
	}
	return neutralize(t, false)
}

// FormatLine renders one checklist line with its marker.
func FormatLine(l Line) string {
	box := "[ ]"
	if l.Checked {
		box = "[x]"
	}
	m := fmt.Sprintf("<!-- ard:fp=%s sev=%s", EncodeField(l.Fingerprint), l.Severity)
	if l.RegressionOf > 0 {
		m += fmt.Sprintf(" reg=%d", l.RegressionOf)
	}
	return fmt.Sprintf("- %s %s %s -->", box, lineText(l), m)
}

const lineMark = " <!-- ard:fp="

// parseLine reads one checklist line; ok is false for anything else.
func parseLine(s string) (Line, bool) {
	var l Line
	switch {
	case strings.HasPrefix(s, "- [ ] "):
	case strings.HasPrefix(s, "- [x] "), strings.HasPrefix(s, "- [X] "):
		l.Checked = true
	default:
		return Line{}, false
	}
	rest := s[len("- [ ] "):]
	i := strings.LastIndex(rest, lineMark)
	if i < 0 || !strings.HasSuffix(rest, " -->") {
		return Line{}, false
	}
	l.Title = rest[:i]
	fields := strings.Fields(rest[i+len(lineMark)-len("fp=") : len(rest)-len(" -->")])
	for _, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return Line{}, false
		}
		switch k {
		case "fp":
			fp, ok := DecodeField(v)
			if !ok || fp == "" {
				return Line{}, false
			}
			l.Fingerprint = fp
		case "sev":
			l.Severity = Severity(v)
		case "reg":
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return Line{}, false
			}
			l.RegressionOf = n
		default:
			return Line{}, false
		}
	}
	if l.Fingerprint == "" || !l.Severity.Valid() {
		return Line{}, false
	}
	return l, true
}

// span returns the text between open and close and its byte range in body
// (from the start of open to the end of close). ok is false when either is
// missing or close precedes open.
func span(body, open, close string) (inner string, start, end int, ok bool) {
	i := strings.Index(body, open)
	if i < 0 {
		return "", 0, 0, false
	}
	j := strings.Index(body[i+len(open):], close)
	if j < 0 {
		return "", 0, 0, false
	}
	j += i + len(open)
	return body[i+len(open) : j], i, j + len(close), true
}

// ParseBody reads the scanner header and the findings span of an issue body.
// Only well-formed marker lines inside the span count; everything else is
// ignored.
func ParseBody(body string) Parsed {
	var p Parsed
	inner, start, _, ok := span(body, findingsOpen, findingsClose)
	head := body
	if ok {
		p.HasSpan = true
		head = body[:start]
		for _, ln := range strings.Split(inner, "\n") {
			if l, ok := parseLine(strings.TrimRight(ln, "\r")); ok {
				p.Lines = append(p.Lines, l)
			}
		}
	}
	p.Scanner, p.Group = parseHeader(head)
	return p
}

// ParseMarkers returns the checklist lines of an issue body. It is the shared
// marker parser for the stale closer (#25) and the Gate 1 duplicate count
// (#26).
func ParseMarkers(body string) []Line { return ParseBody(body).Lines }

func parseHeader(s string) (scanner, group string) {
	const open = "<!-- ard:scanner="
	i := strings.Index(s, open)
	if i < 0 {
		return "", ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, " -->")
	if j < 0 {
		return "", ""
	}
	name, g, ok := strings.Cut(rest[:j], " group=")
	if !ok {
		return "", ""
	}
	grp, ok := DecodeField(g)
	if !ok {
		return "", ""
	}
	return name, grp
}

func header(scanner, group string) string {
	return fmt.Sprintf("<!-- ard:scanner=%s group=%s -->", scanner, EncodeField(group))
}

func findingsSpan(lines []Line) string {
	var b strings.Builder
	b.WriteString(findingsOpen + "\n")
	for _, l := range lines {
		b.WriteString(FormatLine(l) + "\n")
	}
	b.WriteString(findingsClose)
	return b.String()
}

// detailBlock renders one finding's details. limit > 0 truncates the body to
// that many characters.
func detailBlock(f Finding, limit int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<details><summary>%s</summary>\n\n", neutralize(SafeTitle(f.Title), false))
	if f.Body != "" {
		body := f.Body
		if limit > 0 {
			body = truncateRunes(body, limit)
		}
		b.WriteString(neutralize(neutralizeBody(body), true) + "\n\n")
	}
	if len(f.Files) > 0 {
		b.WriteString("Files: ")
		for i, p := range f.Files {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(InlineCode(p))
		}
		b.WriteString("\n\n")
	}
	b.WriteString("</details>\n")
	return b.String()
}

func truncateRunes(s string, n int) string {
	if len(s) <= n { // bytes >= runes, so this is a cheap exit
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// RenderBody builds an issue body. prev is "" for a new issue; otherwise its
// text outside the findings and details spans is kept. The findings span is
// rewritten from p.Lines. add holds the findings whose details are appended to
// the details span (the existing blocks stay); when the body would exceed
// MaxBodyChars the details are trimmed from the end. The checklist is never
// trimmed: group_size bounds it. ard's cost block, when prev has a valid one,
// is cut off before rendering and put back verbatim at the end, so the
// scanner's rewrite never loses or reorders it.
func RenderBody(prev string, p Parsed, add []Finding) string {
	rest, region := costblock.Cut(prev)
	return renderBody(rest, p, add) + region
}

func renderBody(prev string, p Parsed, add []Finding) string {
	fs := findingsSpan(p.Lines)
	var blocks strings.Builder
	for _, f := range add {
		blocks.WriteString(detailBlock(f, MaxDetailChars) + "\n")
	}
	var body string
	if prev == "" {
		intro := fmt.Sprintf("Filed by the `%s` scanner. A box is ticked when a scan no longer reports that finding.", neutralize(p.Scanner, false))
		body = intro + "\n\n" + header(p.Scanner, p.Group) + "\n\n" + fs + "\n\n" + detailsOpen + "\n" + blocks.String() + detailsClose + "\n"
		return trimDetails(body)
	}
	body = prev
	if _, s, e, ok := span(body, findingsOpen, findingsClose); ok {
		body = body[:s] + fs + body[e:]
	} else {
		body = strings.TrimRight(body, "\n") + "\n\n" + fs + "\n"
	}
	if old, s, e, ok := span(body, detailsOpen, detailsClose); ok {
		body = body[:s] + detailsOpen + old + blocks.String() + detailsClose + body[e:]
	} else if blocks.Len() > 0 {
		body = strings.TrimRight(body, "\n") + "\n\n" + detailsOpen + "\n" + blocks.String() + detailsClose + "\n"
	}
	return trimDetails(body)
}

// trimDetails cuts whole detail blocks from the end of the details span until
// the body fits.
func trimDetails(body string) string {
	for len(body) > MaxBodyChars {
		inner, s, e, ok := span(body, detailsOpen, detailsClose)
		if !ok {
			return body
		}
		k := strings.LastIndex(inner, "<details>")
		if k < 0 {
			return body
		}
		body = body[:s] + detailsOpen + inner[:k] + detailsClose + body[e:]
	}
	return body
}

// RenderIssueMD builds .ard/<issue>/issue.md: frontmatter, the checklist with
// its markers, and the untruncated details of the findings in add. prevBody is
// the issue body before this change ("" for a new issue); its details span is
// carried over so a join does not lose the details of earlier findings.
func RenderIssueMD(scanner, sha string, issue int, title string, p Parsed, add []Finding, prevBody string) []byte {
	var b strings.Builder
	b.WriteString("---\nrole: " + scanner + "\nstatus: done\nattempt: 1\n---\n")
	fmt.Fprintf(&b, "# %s\n\nIssue #%d, filed by the `%s` scanner from `%s`.\n\n## Findings\n\n", neutralize(title, false), issue, neutralize(scanner, false), neutralize(sha, false))
	b.WriteString(findingsSpan(p.Lines) + "\n")
	if len(add) > 0 {
		b.WriteString("\n## Details\n\n")
		for _, f := range add {
			b.WriteString(detailBlock(f, 0) + "\n")
		}
	}
	if old, _, _, ok := span(prevBody, detailsOpen, detailsClose); ok && strings.TrimSpace(old) != "" {
		b.WriteString("\n## Earlier details\n\n" + strings.TrimSpace(old) + "\n")
	}
	return []byte(b.String())
}

// IssueTitle is the title of an issue filed for items. A single ungrouped
// finding (or any finding when group_size is 1) uses its own title; a group
// issue uses "<scanner>: findings in <group>" so the title never goes stale
// when a finding joins.
func IssueTitle(scanner, group string, groupSize int, items []Item) string {
	var t string
	if len(items) == 1 && (group == "" || groupSize <= 1) {
		t = items[0].F.Title
	} else {
		t = fmt.Sprintf("%s: findings in %s", scanner, group)
		if group == "" {
			t = scanner + ": findings"
		}
	}
	t = neutralize(SafeTitle(t), false)
	if t == "" {
		t = SafeTitle(scanner)
	}
	if r := []rune(t); len(r) > MaxTitleChars {
		t = string(r[:MaxTitleChars-3]) + "..."
	}
	return t
}
