// Package costblock is the ard-owned cost block in an issue body: a marked
// Markdown table the controller writes from its own records. It renders the
// table, parses it strictly, strips it (so admission and agents never see it)
// and splices it back in. It is a leaf: stdlib only, pure apart from Locks.
//
// The security rule: only text that parses as exactly the table Render emits
// is ever treated as ard's. Strip and Cut touch nothing else, so a fake or
// edited block with prose in it stays visible to the admission hash and to
// agents, and Splice never overwrites it.
package costblock

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	Begin = "<!-- ard:cost:begin -->"
	End   = "<!-- ard:cost:end -->"
	// MaxBlockBytes caps the whole block, markers included.
	MaxBlockBytes = 5000
	// MaxBodyRunes is GitHub's issue body limit.
	MaxBodyRunes = 65536
)

// Tokens are token counts by type.
type Tokens struct{ Input, Output, CacheWrite, CacheRead int64 }

// Row is one closed job. Every field is validated by Render; an invalid
// optional field renders as a dash, so a Row can carry no free text.
type Row struct {
	Stage, Role string   // stage in {triage, worker, review}; role ^[a-z0-9][a-z0-9-]{0,30}$
	Run         int      // 1-based ordinal of this stage:role on the issue; "#k" shown when > 1
	Tokens      *Tokens  // nil: dash
	CostUSD     *float64 // nil: dash, left out of Total
	Models      []string // empty: dash
	Outcome     string   // fixed vocabulary
	Link        string   // "" or a github.com issue/pull URL with an optional fragment
	PR          int      // > 0: " · PR #n"
	// TokensUnreported: the job ended without reporting tokens; the tokens
	// cell says "not reported", or adds " (partial)" when earlier steps did.
	TokensUnreported bool
}

const (
	dash       = "—"
	headerLine = "**Agent cost** · written by ard from its own records; do not edit: an edited block is kept as text, shown to agents and re-gates the issue. Cost is the agent CLI's list-price estimate, not a bill."
	tableHead  = "| agent | tokens | cost | model | comments |"
	tableSep   = "| --- | --- | --- | --- | --- |"
	maxNum     = int64(9_999_949_999_999) // largest count num() renders as "9999.9B" or less
	maxCost    = 1e9
	maxModels  = 8
)

var (
	roleRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)
	modelRE   = regexp.MustCompile(`^[A-Za-z0-9._:\[\]/-]{1,100}$`)
	outcomeRE = regexp.MustCompile(`^(done|changes|stale|needs-human|retry|usage limit|failed: ([a-z_]{1,32}|exit [0-9]{1,3}|result))$`)
	linkRE    = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9._-]{1,100}/[A-Za-z0-9._-]{1,100}/(issues|pull)/[0-9]{1,9}(#(issuecomment|pullrequestreview)-[0-9]{1,18})?$`)

	numPat   = `(?:[0-9]{1,3}|[0-9]{1,4}(?:\.[0-9])?[kMB])`
	tokPat   = `in ` + numPat + ` · out ` + numPat + ` · cache w ` + numPat + ` / r ` + numPat
	rowTok   = `(?:` + tokPat + `(?: \(partial\))?|not reported)`
	costPat  = `\$[0-9]{1,10}\.[0-9]{2}`
	modelPat = `[A-Za-z0-9._:\[\]/-]{1,100}(?:, [A-Za-z0-9._:\[\]/-]{1,100}){0,7}`
	outPat   = `(?:done|changes|stale|needs-human|retry|usage limit|failed: (?:[a-z_]{1,32}|exit [0-9]{1,3}|result))`
	linkPat  = `https://github\.com/[A-Za-z0-9._-]{1,100}/[A-Za-z0-9._-]{1,100}/(?:issues|pull)/[0-9]{1,9}(?:#(?:issuecomment|pullrequestreview)-[0-9]{1,18})?`
	rowRE    = regexp.MustCompile(`^\| (?:(?:triage|worker|review):[a-z0-9][a-z0-9-]{0,30}(?: #[0-9]{1,5})?) \| (?:` + rowTok + `|—) \| (?:` + costPat + `|—) \| (?:` + modelPat + `|—) \| (?:—|(?:\[` + outPat + `\]\(` + linkPat + `\)|` + outPat + `)(?: · PR #[0-9]{1,9})?) \|$`)
	foldRE   = regexp.MustCompile(`^\| \*earlier jobs \([0-9]{1,6}\)\* \| (?:` + tokPat + `|—) \| (?:` + costPat + `|—) \| — \| — \|$`)
	totalRE  = regexp.MustCompile(`^\| \*\*Total\*\* \| \*\*(?:` + tokPat + `|—)\*\* \| \*\*(?:` + costPat + `|—)\*\* \| — \| — \|$`)
	stageOK  = map[string]bool{"triage": true, "worker": true, "review": true}
)

// ValidRow reports whether a row's stage and role are valid; Render drops a
// row that is not. A caller logs such a row.
func ValidRow(r Row) bool { return stageOK[r.Stage] && roleRE.MatchString(r.Role) }

// num formats a count: an integer below 1000, else one decimal (".0" dropped)
// with k, M or B, promoting when rounding reaches 1000.
func num(n int64) string {
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	u := uint64(n)
	units := []struct {
		div uint64 // one tenth of the unit
		suf string
	}{{100, "k"}, {100000, "M"}, {100000000, "B"}}
	for i, un := range units {
		t := (u + un.div/2) / un.div // tenths of the unit
		if t >= 10000 && i < len(units)-1 {
			continue
		}
		s := strconv.FormatUint(t/10, 10)
		if t%10 != 0 {
			s += "." + strconv.FormatUint(t%10, 10)
		}
		return s + un.suf
	}
	return "" // unreachable
}

func tokText(t Tokens) string {
	return fmt.Sprintf("in %s · out %s · cache w %s / r %s", num(t.Input), num(t.Output), num(t.CacheWrite), num(t.CacheRead))
}

func validTokens(t *Tokens) bool {
	if t == nil {
		return false
	}
	for _, v := range []int64{t.Input, t.Output, t.CacheWrite, t.CacheRead} {
		if v < 0 || v > maxNum {
			return false
		}
	}
	return true
}

func validCost(c *float64) bool {
	return c != nil && !math.IsNaN(*c) && *c >= 0 && *c < maxCost
}

// costText formats a cost; c+0 turns -0 into 0.
func costText(c float64) string { return fmt.Sprintf("$%.2f", c+0) }

// sums accumulates the reported tokens and costs of rows.
type sums struct {
	tok  Tokens
	hasT bool
	cost float64
	hasC bool
}

func (s *sums) add(r Row) {
	if validTokens(r.Tokens) {
		s.hasT = true
		s.tok.Input += r.Tokens.Input
		s.tok.Output += r.Tokens.Output
		s.tok.CacheWrite += r.Tokens.CacheWrite
		s.tok.CacheRead += r.Tokens.CacheRead
	}
	if validCost(r.CostUSD) {
		s.hasC = true
		s.cost += *r.CostUSD
	}
}

func (s sums) cells() (tok, cost string) {
	tok, cost = dash, dash
	if s.hasT && validTokens(&s.tok) {
		tok = tokText(s.tok)
	}
	if s.hasC && s.cost < 9_999_999_999 {
		cost = costText(s.cost)
	}
	return
}

func rowLine(r Row) string {
	agent := r.Stage + ":" + r.Role
	if r.Run > 1 && r.Run <= 99999 {
		agent += " #" + strconv.Itoa(r.Run)
	}
	tok, cost := dash, dash
	if validTokens(r.Tokens) {
		tok = tokText(*r.Tokens)
		if r.TokensUnreported {
			tok += " (partial)"
		}
	} else if r.TokensUnreported {
		tok = "not reported"
	}
	if validCost(r.CostUSD) {
		cost = costText(*r.CostUSD)
	}
	var models []string
	seen := map[string]bool{}
	for _, m := range r.Models {
		if modelRE.MatchString(m) && !seen[m] && len(models) < maxModels {
			seen[m] = true
			models = append(models, m)
		}
	}
	model := dash
	if len(models) > 0 {
		model = strings.Join(models, ", ")
	}
	comments := dash
	if outcomeRE.MatchString(r.Outcome) {
		comments = r.Outcome
		if linkRE.MatchString(r.Link) {
			comments = "[" + r.Outcome + "](" + r.Link + ")"
		}
		if r.PR > 0 && r.PR <= 999999999 {
			comments += " · PR #" + strconv.Itoa(r.PR)
		}
	}
	return fmt.Sprintf("| %s | %s | %s | %s | %s |", agent, tok, cost, model, comments)
}

func foldLine(k int, s sums) string {
	tok, cost := s.cells()
	return fmt.Sprintf("| *earlier jobs (%d)* | %s | %s | %s | %s |", k, tok, cost, dash, dash)
}

func totalLine(s sums) string {
	tok, cost := s.cells()
	return fmt.Sprintf("| **Total** | **%s** | **%s** | %s | %s |", tok, cost, dash, dash)
}

// Render returns the whole block, markers included, at most maxBytes long. It
// folds the oldest rows into one "earlier jobs (k)" row until it fits; Total
// always covers every row. Rows with an invalid stage or role are dropped.
// ok is false when no row is left or even the fully folded block does not fit.
// Render never emits a block Valid rejects.
func Render(rows []Row, maxBytes int) (block string, folded int, ok bool) {
	var kept []Row
	for _, r := range rows {
		if ValidRow(r) {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		return "", 0, false
	}
	lines := make([]string, len(kept))
	var total sums
	for i, r := range kept {
		lines[i] = rowLine(r)
		total.add(r)
	}
	tl := totalLine(total)
	pre := Begin + "\n" + headerLine + "\n\n" + tableHead + "\n" + tableSep + "\n"
	post := tl + "\n" + End
	size := len(pre) + len(post)
	rest := 0
	for _, l := range lines {
		rest += len(l) + 1
	}
	var fold sums
	for k := 0; k <= len(kept); k++ {
		if k > 0 {
			fold.add(kept[k-1])
			rest -= len(lines[k-1]) + 1
		}
		fl := 0
		if k > 0 {
			fl = len(foldLine(k, fold)) + 1
		}
		if size+fl+rest > maxBytes {
			continue
		}
		var b strings.Builder
		b.WriteString(pre)
		if k > 0 {
			b.WriteString(foldLine(k, fold) + "\n")
		}
		for _, l := range lines[k:] {
			b.WriteString(l + "\n")
		}
		b.WriteString(post)
		return b.String(), k, true
	}
	return "", 0, false
}

// Valid reports whether the text between the markers is exactly a table
// Render could emit. LF or CRLF line ends are both accepted.
func Valid(inner string) bool {
	if strings.Contains(inner, "\r\n") {
		inner = strings.ReplaceAll(inner, "\r\n", "\n")
	}
	if strings.ContainsRune(inner, '\r') || len(inner) < 2 || inner[0] != '\n' || inner[len(inner)-1] != '\n' {
		return false
	}
	if len(inner)+len(Begin)+len(End) > MaxBlockBytes {
		return false
	}
	ls := strings.Split(inner[1:len(inner)-1], "\n")
	if len(ls) < 6 || ls[0] != headerLine || ls[1] != "" || ls[2] != tableHead || ls[3] != tableSep || !totalRE.MatchString(ls[len(ls)-1]) {
		return false
	}
	mid := ls[4 : len(ls)-1]
	for i, l := range mid {
		if rowRE.MatchString(l) || (i == 0 && foldRE.MatchString(l)) {
			continue
		}
		return false
	}
	return true
}

// Locate finds the owned region: e is the last End and s the last Begin before
// it. sepStart is the start of the "\n\n" or "\r\n\r\n" directly before s (s
// itself when there is none). end is the index just past End.
func Locate(body string) (sepStart, start, end int, ok bool) {
	e := strings.LastIndex(body, End)
	if e < 0 {
		return 0, 0, 0, false
	}
	s := strings.LastIndex(body[:e], Begin)
	if s < 0 {
		return 0, 0, 0, false
	}
	sep := s
	switch {
	case strings.HasSuffix(body[:s], "\r\n\r\n"):
		sep = s - 4
	case strings.HasSuffix(body[:s], "\n\n"):
		sep = s - 2
	}
	return sep, s, e + len(End), true
}

// locateValid is Locate, only when the region parses as ard's own table.
func locateValid(body string) (sepStart, start, end int, ok bool) {
	sepStart, start, end, ok = Locate(body)
	if !ok || !Valid(body[start+len(Begin):end-len(End)]) {
		return 0, 0, 0, false
	}
	return
}

// Region returns the located region (markers included) when it parses as
// ard's table, else "".
func Region(body string) string {
	_, s, e, ok := locateValid(body)
	if !ok {
		return ""
	}
	return body[s:e]
}

// Strip removes the located region and its separator, only when it is Valid.
// Otherwise it returns body unchanged, so a body without ard's block hashes and
// reads exactly as before.
func Strip(body string) string {
	rest, _ := Cut(body)
	return rest
}

// Cut splits a valid region (with its separator) off body: rest is body without
// it and region is the removed text, "" when there is none (rest is then body).
func Cut(body string) (rest, region string) {
	sep, _, end, ok := locateValid(body)
	if !ok {
		return body, ""
	}
	return body[:sep] + body[end:], body[sep:end]
}

// Splice puts block in place of the region when that region is ard's own
// (Valid). A located region that is not ard's table is human text: it is kept
// and block is appended after it, so no human text is ever dropped. With no
// region it returns body + "\n\n" + block (just block for an empty body).
func Splice(body, block string) string {
	if _, s, e, ok := locateValid(body); ok {
		return body[:s] + block + body[e:]
	}
	if body == "" {
		return block
	}
	return body + "\n\n" + block
}
