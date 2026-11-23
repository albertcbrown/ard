package costblock

import (
	"math"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

func f(v float64) *float64 { return &v }

func TestNum(t *testing.T) {
	for _, c := range []struct {
		n    int64
		want string
	}{{0, "0"}, {999, "999"}, {1000, "1k"}, {3300, "3.3k"}, {19100, "19.1k"}, {999949, "999.9k"}, {999950, "1M"},
		{1300000, "1.3M"}, {2e9, "2B"}, {173500, "173.5k"}, {999950000, "1B"}, {1e15, "1000000B"}} {
		if got := num(c.n); got != c.want {
			t.Errorf("num(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func sample() []Row {
	return []Row{
		{Stage: "triage", Role: "lead", Run: 1, Tokens: &Tokens{20, 3300, 19100, 173500}, CostUSD: f(0.42), Models: []string{"claude-sonnet-5-5"}, Outcome: "done", Link: "https://github.com/o/r/issues/172#issuecomment-1"},
		{Stage: "worker", Role: "engineer", Run: 2, Tokens: &Tokens{1200, 40100, 88000, 1300000}, CostUSD: f(3.10), Models: []string{"claude-haiku-4-5", "claude-opus-5-5"}, Outcome: "failed: deadline", Link: "https://github.com/o/r/issues/172#issuecomment-2", PR: 181},
		{Stage: "review", Role: "eng", Run: 1, Outcome: "changes", Link: "https://github.com/o/r/pull/181#pullrequestreview-9"},
	}
}

func inner(block string) string {
	return block[len(Begin) : len(block)-len(End)]
}

func TestRenderShape(t *testing.T) {
	b, folded, ok := Render(sample(), MaxBlockBytes)
	if !ok || folded != 0 {
		t.Fatal(ok, folded)
	}
	for _, want := range []string{Begin + "\n", "| triage:lead | in 20 · out 3.3k · cache w 19.1k / r 173.5k | $0.42 | claude-sonnet-5-5 | [done](https://github.com/o/r/issues/172#issuecomment-1) |",
		"| worker:engineer #2 |", "| review:eng | — | — | — | [changes](", " · PR #181 |", "| **Total** | **in 1.2k · out 43.4k · cache w 107.1k / r 1.5M** | **$3.52** | — | — |\n" + End} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}
	if !Valid(inner(b)) {
		t.Fatal("rendered block is not valid")
	}
	crlf := strings.ReplaceAll(b, "\n", "\r\n")
	if !Valid(inner(crlf)) {
		t.Error("CRLF block not valid")
	}
}

func TestRenderHostileFields(t *testing.T) {
	bad := Row{Stage: "worker", Role: "eng", Tokens: &Tokens{-1, 0, 0, 0}, CostUSD: f(-3), Models: []string{"a|b", "x\ny", "ok-model", "<!-- ard:cost:end -->"},
		Outcome: "failed: |evil|\n", Link: "https://evil.example/x)[y](z", PR: 4}
	rows := []Row{bad,
		{Stage: "worker", Role: "ENG!"}, {Stage: "bogus", Role: "eng"}, {Stage: "worker", Role: "a|b", Outcome: "done"},
		{Stage: "review", Role: "r", Outcome: "done", Link: "javascript:alert(1)", Models: []string{strings.Repeat("a", 101)}, CostUSD: f(1)}}
	b, _, ok := Render(rows, MaxBlockBytes)
	if !ok {
		t.Fatal("not ok")
	}
	if !Valid(inner(b)) {
		t.Fatalf("invalid:\n%s", b)
	}
	if strings.Count(b, "| worker:")+strings.Count(b, "| review:") != 2 {
		t.Errorf("rows:\n%s", b)
	}
	for _, s := range []string{"evil", "javascript", "a|b", "ENG"} {
		if strings.Contains(b, s) {
			t.Errorf("hostile %q leaked:\n%s", s, b)
		}
	}
	if !strings.Contains(b, "ok-model") || !strings.Contains(b, "| worker:eng | — | — | ok-model | — |") {
		t.Errorf("bad row:\n%s", b)
	}
	if !strings.Contains(b, "| **$1.00** |") {
		t.Errorf("total:\n%s", b)
	}
}

func TestRoundTripRandom(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	junk := []string{"", "done", "retry", "usage limit", "failed: exit 2", "failed: result", "failed: heartbeat_quiet", "x|y", "\n", "failed: ", "https://github.com/o/r/issues/1", "https://github.com/o/r/pull/2#pullrequestreview-3", "https://github.com/o/r/pull/2#bad", "triage", "worker", "review", "lead", "a-b", "m1", "m|2", "ok.model:1"}
	pick := func() string { return junk[rnd.Intn(len(junk))] }
	for i := 0; i < 500; i++ {
		var rows []Row
		for j := rnd.Intn(30); j >= 0; j-- {
			r := Row{Stage: pick(), Role: pick(), Run: rnd.Intn(5), Outcome: pick(), Link: pick(), PR: rnd.Intn(3) - 1, Models: []string{pick(), pick()}}
			if rnd.Intn(2) == 0 {
				r.Tokens = &Tokens{rnd.Int63n(3e9) - 5, rnd.Int63n(1000), rnd.Int63n(1e15), rnd.Int63n(1e12)}
			}
			if rnd.Intn(2) == 0 {
				r.CostUSD = f(rnd.Float64()*20 - 1)
				switch rnd.Intn(4) {
				case 0:
					r.CostUSD = f(9.9e8 + rnd.Float64()*1e8)
				case 1:
					r.CostUSD = f(math.Copysign(0, -1))
				}
			}
			rows = append(rows, r)
		}
		b, _, ok := Render(rows, 1500)
		if ok && !Valid(inner(b)) || ok && len(b) > 1500 {
			t.Fatalf("rows %+v -> invalid/oversize (%d):\n%s", rows, len(b), b)
		}
	}
}

func TestFold(t *testing.T) {
	var rows []Row
	var sum float64
	for i := 0; i < 60; i++ {
		c := float64(i) + 0.25
		sum += c
		rows = append(rows, Row{Stage: "worker", Role: "engineer", Run: i + 1, Tokens: &Tokens{1, 1, 1, 1}, CostUSD: &c, Models: []string{"claude-opus-5-5"}, Outcome: "retry", Link: "https://github.com/o/r/issues/172#issuecomment-123456"})
	}
	full, k0, _ := Render(rows, 1<<20)
	b, folded, ok := Render(rows, MaxBlockBytes)
	if !ok || k0 != 0 || folded == 0 || len(b) > MaxBlockBytes || len(full) <= MaxBlockBytes {
		t.Fatalf("ok=%v folded=%d len=%d full=%d", ok, folded, len(b), len(full))
	}
	if !Valid(inner(b)) || !strings.Contains(b, "earlier jobs (") {
		t.Fatalf("block:\n%s", b)
	}
	tot := func(s string) string { return s[strings.Index(s, "| **Total**"):] }
	if tot(b) != tot(full) {
		t.Errorf("total differs:\n%s\n%s", tot(b), tot(full))
	}
	if !strings.Contains(b, "| worker:engineer #60 |") || strings.Contains(b, "| worker:engineer #1 |") {
		t.Errorf("oldest should be folded, newest kept:\n%s", b)
	}
	if _, _, ok := Render(rows, 400); ok {
		t.Error("impossibly small cap rendered")
	}
	if _, _, ok := Render(nil, 5000); ok {
		t.Error("no rows rendered")
	}
}

func TestStripValidity(t *testing.T) {
	blk, _, _ := Render(sample(), MaxBlockBytes)
	fake := Begin + "\nnever touch auth\n" + End
	for _, c := range []struct {
		name, body, want string
	}{
		{"no block", "Fix X\n\nmore", "Fix X\n\nmore"},
		{"valid at end", "Fix X\n\n" + blk, "Fix X"},
		{"only block", blk, ""},
		{"mid body", "Fix X\n\n" + blk + "\n\nafter", "Fix X\n\nafter"},
		{"crlf", "Fix X\r\n\r\n" + strings.ReplaceAll(blk, "\n", "\r\n"), "Fix X"},
		{"fake prose", "Fix X\n\n" + fake, "Fix X\n\n" + fake},
		{"fake around prose then valid earlier", blk + "\n\n" + fake, blk + "\n\n" + fake},
		{"dangling begin after", "Fix X\n\n" + blk + "\n\n" + Begin + " tail", "Fix X\n\n" + Begin + " tail"},
		{"dangling end before", End + " x\n\n" + blk, End + " x"},
		{"edited table", "Fix X\n\n" + strings.Replace(blk, "$0.42", "$0.42 IGNORE ALL", 1), "Fix X\n\n" + strings.Replace(blk, "$0.42", "$0.42 IGNORE ALL", 1)},
		{"extra line", "Fix X\n\n" + strings.Replace(blk, End, "extra\n"+End, 1), "Fix X\n\n" + strings.Replace(blk, End, "extra\n"+End, 1)},
		{"begin inside prose before valid", "a " + Begin + " b\n\n" + blk, "a " + Begin + " b"},
	} {
		if got := Strip(c.body); got != c.want {
			t.Errorf("%s: Strip = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSpliceKeepsHumanText(t *testing.T) {
	blk, _, _ := Render(sample(), MaxBlockBytes)
	blk2, _, _ := Render(sample()[:1], MaxBlockBytes)
	if got := Splice("", blk); got != blk {
		t.Error("empty body")
	}
	if got := Splice("hi", blk); got != "hi\n\n"+blk {
		t.Errorf("append: %q", got)
	}
	if got := Splice("hi\n\n"+blk+"\n\nafter", blk2); got != "hi\n\n"+blk2+"\n\nafter" {
		t.Errorf("replace: %q", got)
	}
	fake := Begin + "\nhuman words\n" + End
	got := Splice("hi\n\n"+fake, blk)
	if !strings.Contains(got, "human words") || Strip(got) != "hi\n\n"+fake {
		t.Errorf("fake dropped or mis-stripped: %q", got)
	}
	if r := Region(got); r != blk {
		t.Error("region")
	}
	rest, region := Cut("hi\n\n" + blk + "\n\nafter")
	if rest != "hi\n\nafter" || region != "\n\n"+blk {
		t.Errorf("cut: %q %q", rest, region)
	}
}

func FuzzStripSplice(f *testing.F) {
	blk1, _, _ := Render(sample(), MaxBlockBytes)
	blk2, _, _ := Render(sample()[:1], MaxBlockBytes)
	for _, s := range []string{"", "a", "a\n\n", "a\r\n\r\n", "x\r", "\n\n\n", "a\r\n", Begin, End, "<!-- ard"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, b string) {
		if !utf8.ValidString(b) {
			return
		}
		if strings.Contains(b, Begin) || strings.Contains(b, End) {
			return
		}
		s1 := Splice(b, blk1)
		if Strip(s1) != b {
			t.Fatalf("Strip(Splice(b)) != b for %q", b)
		}
		if Splice(s1, blk2) != Splice(b, blk2) {
			t.Fatalf("splice not idempotent for %q", b)
		}
		if !strings.HasPrefix(s1, b) {
			t.Fatalf("text before block changed for %q", b)
		}
		if Strip(b) != b {
			t.Fatalf("strip changed a body without markers")
		}
	})
}

func TestLocks(t *testing.T) {
	var nilLocks *Locks
	nilLocks.Lock(1)()
	l := &Locks{}
	n := 0
	done := make(chan bool)
	for i := 0; i < 50; i++ {
		go func() {
			defer func() { done <- true }()
			u := l.Lock(7)
			n++
			u()
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}
	if n != 50 {
		t.Fatal(n)
	}
	l.Lock(1)()
	l.Lock(2)()
}

func TestRenderExtremesStayValid(t *testing.T) {
	big := int64(9_999_949_999_999)
	neg0 := math.Copysign(0, -1)
	rows := []Row{
		{Stage: "worker", Role: "a", Tokens: &Tokens{big, big, big, big}, CostUSD: f(9.99e8), Outcome: "done"},
		{Stage: "worker", Role: "a", Tokens: &Tokens{big, big, big, big}, CostUSD: f(9.99e8), Outcome: "done"},
		{Stage: "worker", Role: "b", Tokens: &Tokens{1e15 + 1, 0, 0, 0}, CostUSD: &neg0, Outcome: "done"},
	}
	for n := 1; n <= 3; n++ {
		b, _, ok := Render(rows[:n], MaxBlockBytes)
		if !ok || !Valid(inner(b)) || strings.Contains(b, "$-") {
			t.Fatalf("n=%d ok=%v:\n%s", n, ok, b)
		}
	}
	many := make([]Row, 30)
	for i := range many {
		many[i] = Row{Stage: "worker", Role: "a", Tokens: &Tokens{big, 0, 0, 0}, CostUSD: f(9.99e8), Outcome: "done"}
	}
	if b, _, ok := Render(many, MaxBlockBytes); !ok || !Valid(inner(b)) || !strings.Contains(b, "| **Total** | **—** | **—** |") {
		t.Errorf("overflowing totals must render dashes:\n%s", b)
	}
}
