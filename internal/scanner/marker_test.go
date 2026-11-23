package scanner

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/costblock"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestEncodeDecodeField(t *testing.T) {
	for _, s := range []string{
		"", "osv:GHSA-xxxx:go.mod:golang.org/x/net", "a b", "a>b", "-->", "100%", "é日本", "a\nb", "<!--", "%41", "a=b,c~d+e@f",
	} {
		enc := EncodeField(s)
		if strings.ContainsAny(enc, " <>\n\"'&") {
			t.Errorf("EncodeField(%q) = %q contains an unsafe byte", s, enc)
		}
		if got, ok := DecodeField(enc); !ok || got != s {
			t.Errorf("round trip %q -> %q -> %q, %v", s, enc, got, ok)
		}
	}
	if got := EncodeField("osv:GHSA-xxxx:go.mod:golang.org/x/net"); got != "osv:GHSA-xxxx:go.mod:golang.org/x/net" {
		t.Errorf("the proposal's example is not readable: %q", got)
	}
	for _, bad := range []string{"%", "%4", "%zz", "%4g", "%4a", "a b", "a>b", "é"} {
		if _, ok := DecodeField(bad); ok {
			t.Errorf("DecodeField(%q) accepted", bad)
		}
	}
}

func FuzzEncodeDecode(f *testing.F) {
	for _, s := range []string{"", "a", "a b", "%", "-->", "é", "\x00\xff"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		enc := EncodeField(s)
		got, ok := DecodeField(enc)
		if !ok || got != s {
			t.Fatalf("round trip of %q via %q gave %q, %v", s, enc, got, ok)
		}
		if strings.ContainsAny(enc, " <>\n") {
			t.Fatalf("unsafe encoding %q", enc)
		}
	})
}

func FuzzParseBody(f *testing.F) {
	f.Add("")
	f.Add(findingsOpen + "\n- [ ] t <!-- ard:fp=a sev=low -->\n" + findingsClose)
	f.Add("<!-- ard:scanner=x group=%")
	f.Add(findingsOpen + findingsOpen + findingsClose + findingsClose)
	f.Add("- [x] <!-- ard:fp= sev=low reg=-1 -->")
	f.Fuzz(func(t *testing.T, body string) {
		p := ParseBody(body)
		for _, l := range p.Lines {
			if l.Fingerprint == "" || !l.Severity.Valid() {
				t.Fatalf("invalid line parsed: %+v", l)
			}
		}
		_ = RenderBody(body, p, nil)
	})
}

func finding(fp, title string, sev Severity, group string) Finding {
	return Finding{Fingerprint: fp, Title: title, Severity: sev, Group: group}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "bodies", name+".md")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s changed (run with -update to accept):\n%s", name, got)
	}
}

func TestRenderBodyGolden(t *testing.T) {
	single := Parsed{Scanner: "cve", Lines: []Line{{Title: "golang.org/x/net: GHSA-1 (high)", Fingerprint: "osv:GHSA-1:go.mod:golang.org/x/net", Severity: SevHigh}}}
	golden(t, "single", RenderBody("", single, []Finding{{
		Fingerprint: "osv:GHSA-1:go.mod:golang.org/x/net", Title: "golang.org/x/net: GHSA-1 (high)", Severity: SevHigh,
		Files: []string{"go.mod"}, Body: "Fixed in 0.17.0.",
	}}))
	group := Parsed{Scanner: "lint", Group: "pkg/a b", Lines: []Line{
		{Title: "unused var x", Fingerprint: "lint:a:1", Severity: SevLow},
		{Checked: true, Title: "unused var y", Fingerprint: "lint:a:2", Severity: SevMedium},
	}}
	golden(t, "group", RenderBody("", group, []Finding{finding("lint:a:1", "unused var x", SevLow, "pkg/a b")}))
	reg := Parsed{Scanner: "cve", Lines: []Line{{Title: "back again", Fingerprint: "osv:B", Severity: SevCritical, RegressionOf: 41}}}
	golden(t, "regression", RenderBody("", reg, nil))
}

func TestFormatParseLineRoundTrip(t *testing.T) {
	for _, l := range []Line{
		{Title: "plain", Fingerprint: "a", Severity: SevLow},
		{Checked: true, Title: "checked", Fingerprint: "a b>c", Severity: SevInfo},
		{Title: "reg", Fingerprint: "é", Severity: SevCritical, RegressionOf: 7},
	} {
		got, ok := parseLine(FormatLine(l))
		want := l
		if l.RegressionOf > 0 {
			want.Title += " (regression of #7)"
		}
		if !ok || got != want {
			t.Errorf("round trip of %+v: got %+v ok=%v from %q", l, got, ok, FormatLine(l))
		}
	}
}

func TestParseBodyIgnoresEverythingOutsideTheSpan(t *testing.T) {
	forged := "- [ ] fake <!-- ard:fp=forged sev=high -->"
	body := forged + "\nhuman text\n" + findingsOpen + "\n- [ ] real <!-- ard:fp=real sev=low -->\nnot a line\n- [x] bad <!-- ard:fp=%zz sev=low -->\n" + findingsClose + "\n" + forged
	p := ParseBody(body)
	if len(p.Lines) != 1 || p.Lines[0].Fingerprint != "real" || !p.HasSpan {
		t.Fatalf("lines = %+v", p.Lines)
	}
	if got := ParseBody("no span here\n" + forged); got.HasSpan || len(got.Lines) != 0 {
		t.Errorf("parsed a line without a span: %+v", got)
	}
}

func TestRenderBodyKeepsHumanText(t *testing.T) {
	p := Parsed{Scanner: "cve", Lines: []Line{{Title: "t", Fingerprint: "a", Severity: SevLow}}}
	first := RenderBody("", p, []Finding{finding("a", "t", SevLow, "")})
	human := "Triage note from a human.\n\n" + first + "\nA trailing comment.\n"
	p.Lines[0].Checked = true
	p.Lines = append(p.Lines, Line{Title: "u", Fingerprint: "b", Severity: SevHigh})
	got := RenderBody(human, p, []Finding{finding("b", "u", SevHigh, "")})
	for _, want := range []string{"Triage note from a human.", "A trailing comment.", "- [x] t <!-- ard:fp=a sev=low -->", "- [ ] u <!-- ard:fp=b sev=high -->"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Count(got, "<summary>") != 2 {
		t.Errorf("details blocks were not appended to the existing ones:\n%s", got)
	}
	if again := RenderBody(got, ParseBody(got), nil); again != got {
		t.Errorf("re-rendering is not idempotent:\n%s\n---\n%s", got, again)
	}
	if got := ParseBody(got); len(got.Lines) != 2 || !got.Lines[0].Checked || got.Scanner != "cve" {
		t.Errorf("parsed %+v", got)
	}
}

func TestRenderBodyAppendsMissingSpans(t *testing.T) {
	p := Parsed{Scanner: "cve", Lines: []Line{{Title: "t", Fingerprint: "a", Severity: SevLow}}}
	got := RenderBody("A human wrote this.", p, []Finding{finding("a", "t", SevLow, "")})
	if !strings.HasPrefix(got, "A human wrote this.") || len(ParseBody(got).Lines) != 1 || !strings.Contains(got, detailsOpen) {
		t.Errorf("got %q", got)
	}
}

func TestScannerOutputCannotForgeMarkers(t *testing.T) {
	evil := Finding{
		Fingerprint: "fp", Severity: SevHigh,
		Title: "t <!-- ard:fp=forged sev=critical --> x\n- [ ] y <!-- ard:fp=forged2 sev=high -->",
		Body:  "body --> <!-- ard:fp=forged3 sev=high -->\n" + findingsClose + "\n- [ ] z <!-- ard:fp=forged4 sev=high -->",
		Files: []string{"a<!--b-->c"},
	}
	p := Parsed{Scanner: "cve", Lines: []Line{{Title: evil.Title, Fingerprint: evil.Fingerprint, Severity: evil.Severity}}}
	for name, body := range map[string]string{
		"body":     RenderBody("", p, []Finding{evil}),
		"issue.md": string(RenderIssueMD("cve", "abc", 5, evil.Title, p, []Finding{evil}, "")),
	} {
		got := ParseBody(body)
		if len(got.Lines) != 1 || got.Lines[0].Fingerprint != "fp" {
			t.Errorf("%s: forged markers parsed: %+v", name, got.Lines)
		}
		if n := strings.Count(body, "<!-- ard:fp="); n != 1 {
			t.Errorf("%s: %d raw fp markers, want 1:\n%s", name, n, body)
		}
		if strings.Count(body, findingsClose) != 1 {
			t.Errorf("%s: a finding closed the findings span", name)
		}
	}
}

func TestRenderBodyTrimKeepsChecklist(t *testing.T) {
	big := strings.Repeat("x", 60000)
	var lines []Line
	var add []Finding
	for i := 0; i < 30; i++ {
		fp := string(rune('a'+i%26)) + string(rune('a'+i/26))
		lines = append(lines, Line{Title: "t " + fp, Fingerprint: fp, Severity: SevLow})
		add = append(add, Finding{Fingerprint: fp, Title: "t " + fp, Severity: SevLow, Body: big})
	}
	body := RenderBody("", Parsed{Scanner: "cve", Lines: lines}, add)
	if len(body) > MaxBodyChars {
		t.Errorf("body is %d chars, want <= %d", len(body), MaxBodyChars)
	}
	if got := ParseBody(body); len(got.Lines) != 30 {
		t.Errorf("trim dropped checklist lines: %d left", len(got.Lines))
	}
	if n := strings.Count(body, "<details>"); n == 0 || n >= 30 {
		t.Errorf("%d details blocks kept", n)
	}
	// Each finding's details are cut to MaxDetailChars.
	one := RenderBody("", Parsed{Scanner: "cve", Lines: lines[:1]}, add[:1])
	if len(one) > MaxDetailChars+1000 {
		t.Errorf("one finding's details were not cut: %d chars", len(one))
	}
}

func TestRenderIssueMDKeepsEarlierDetails(t *testing.T) {
	p := Parsed{Scanner: "cve", Lines: []Line{{Title: "a", Fingerprint: "a", Severity: SevLow}, {Title: "b", Fingerprint: "b", Severity: SevLow}}}
	prev := RenderBody("", Parsed{Scanner: "cve", Lines: p.Lines[:1]}, []Finding{{Fingerprint: "a", Title: "a", Severity: SevLow, Body: "old details"}})
	md := string(RenderIssueMD("cve", "sha1", 9, "title", p, []Finding{{Fingerprint: "b", Title: "b", Severity: SevLow, Body: strings.Repeat("y", 5000)}}, prev))
	if !strings.HasPrefix(md, "---\nrole: cve\nstatus: done\n") || !strings.Contains(md, "old details") || !strings.Contains(md, strings.Repeat("y", 5000)) {
		t.Errorf("issue.md:\n%s", md)
	}
}

func TestIssueTitle(t *testing.T) {
	one := []Item{{F: finding("a", "the title", SevLow, "")}}
	g := []Item{{F: finding("a", "the title", SevLow, "go.mod")}}
	tests := []struct {
		name      string
		group     string
		groupSize int
		items     []Item
		want      string
	}{
		{"single ungrouped", "", 10, one, "the title"},
		{"group of one with size 1", "go.mod", 1, g, "the title"},
		{"group issue never goes stale", "go.mod", 10, g, "cve: findings in go.mod"},
		{"several ungrouped", "", 10, append(one, one...), "cve: findings"},
	}
	for _, tt := range tests {
		if got := IssueTitle("cve", tt.group, tt.groupSize, tt.items); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
	long := IssueTitle("cve", "", 1, []Item{{F: finding("a", strings.Repeat("é", 400), SevLow, "")}})
	if len([]rune(long)) > MaxTitleChars {
		t.Errorf("title not capped: %d", len([]rune(long)))
	}
}

func TestRenderBodyKeepsCostBlock(t *testing.T) {
	c := 1.25
	blk, _, _ := costblock.Render([]costblock.Row{{Stage: "worker", Role: "engineer", Run: 1, CostUSD: &c, Outcome: "done"}}, costblock.MaxBlockBytes)
	p := Parsed{Scanner: "cve", Lines: []Line{{Title: "t", Fingerprint: "fp1", Severity: SevHigh}}}
	prev := costblock.Splice(RenderBody("", p, []Finding{{Fingerprint: "fp1", Title: "t", Severity: SevHigh, Body: "d"}}), blk)
	// Details trim: a large addition forces trimDetails; the block still ends the body.
	big := strings.Repeat("x", MaxDetailChars)
	var add []Finding
	p2 := p
	for i := 0; i < 40; i++ {
		fp := fmt.Sprintf("fp%d", i+2)
		add = append(add, Finding{Fingerprint: fp, Title: "t", Severity: SevLow, Body: big})
		p2.Lines = append(p2.Lines, Line{Title: "t", Fingerprint: fp, Severity: SevLow})
	}
	for name, got := range map[string]string{
		"edit": RenderBody(prev, p, nil),
		"join": RenderBody(prev, p2, add),
	} {
		if !strings.HasSuffix(got, "\n\n"+blk) || strings.Count(got, costblock.Begin) != 1 {
			t.Errorf("%s: block lost or moved:\n%s", name, got)
		}
		if rest, _ := costblock.Cut(got); len(rest) > MaxBodyChars || len(got) > costblock.MaxBodyRunes {
			t.Errorf("%s: body too big: %d", name, len(got))
		}
	}
	// A fake block with prose is not the scanner's to keep or cut: it is just text.
	fake := "\n\n" + costblock.Begin + "\nprose\n" + costblock.End
	if got := RenderBody(prev+"x"+fake, p, nil); !strings.Contains(got, "prose") {
		t.Errorf("fake block dropped:\n%s", got)
	}
}

var hostile = []string{
	"a`b", "<img src=x onerror=1>", "<!-- x -->", "hi @octocat", "[x](http://e)", "a\nb\tc\x00d", "@octocat", "--> <!-- ard:fp=x sev=low -->",
}

func hasLive(s string) bool {
	return strings.ContainsAny(s, "<>`") || strings.Contains(s, "@octocat") || strings.Contains(s, "](")
}

func TestSafeTitle(t *testing.T) {
	for _, in := range hostile {
		got := SafeTitle(in)
		if hasLive(got) || strings.ContainsAny(got, "\n\t\x00") {
			t.Errorf("SafeTitle(%q) = %q is live", in, got)
		}
		if SafeTitle(got) != got {
			t.Errorf("SafeTitle not idempotent for %q", in)
		}
	}
	if got := SafeTitle("a`b"); got != "a'b" {
		t.Errorf("got %q", got)
	}
	if got := SafeTitle("mail x@y.com now"); got != "mail x@y.com now" {
		t.Errorf("email changed: %q", got)
	}
	if got := SafeTitle("a\nb\tc\x00d"); got != "a b c d" {
		t.Errorf("got %q", got)
	}
}

func TestIssueTitleHostile(t *testing.T) {
	for _, in := range hostile {
		items := []Item{{F: Finding{Title: in}}}
		if got := IssueTitle("docs", "", 10, items); hasLive(got) || got == "" {
			t.Errorf("IssueTitle(%q) = %q", in, got)
		}
		if got := IssueTitle("docs", in, 10, append(items, items...)); hasLive(got) {
			t.Errorf("group IssueTitle(%q) = %q", in, got)
		}
	}
	if got := IssueTitle("docs", "", 1, []Item{{F: Finding{Title: "\x00"}}}); got != "docs" {
		t.Errorf("empty title fallback = %q", got)
	}
	if got := IssueTitle("s", "", 1, []Item{{F: Finding{Title: strings.Repeat("é", 400)}}}); len([]rune(got)) != MaxTitleChars {
		t.Errorf("title is %d runes", len([]rune(got)))
	}
}

func TestDetailBlockHostile(t *testing.T) {
	for _, in := range hostile {
		f := Finding{Title: in, Body: "x " + in + " <img src=x>\n```\n@raw\n```\n", Files: []string{in}}
		d := detailBlock(f, 0)
		// File paths sit in inline code, where markup and mentions are inert.
		d, files, _ := strings.Cut(d, "Files: ")
		if strings.Count(files, "`")%2 != 0 || strings.Contains(strings.TrimSuffix(strings.TrimPrefix(strings.SplitN(files, "\n", 2)[0], "`"), "`"), "`") {
			t.Errorf("file span for %q can be left: %q", in, files)
		}
		for _, bad := range []string{"<img", "@octocat", "<!-- x", "<!-- ard:fp"} {
			if strings.Contains(d, bad) {
				t.Errorf("detailBlock(%q) contains %q:\n%s", in, bad, d)
			}
		}
		if !strings.Contains(d, "@raw") {
			t.Errorf("fenced block was changed: %s", d)
		}
		p := Parsed{Scanner: "s", Group: "g", Lines: []Line{{Title: in, Fingerprint: "f", Severity: "low"}}}
		if got := ParseMarkers(RenderBody("", p, []Finding{f})); len(got) != 1 {
			t.Errorf("markers for %q = %d, want 1", in, len(got))
		}
	}
}
