package scanner

import "testing"

func TestSimilarTitles(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"Missing label", "missing LABEL!", true},
		{"missing label", "missing labels", false},
		{"fix cache", "fix cache bug", false}, // 2 vs 3 tokens, not equal
		{"cache bug", "cache bug", true},
		{"a b c d e", "a b c d e", false}, // one-character tokens drop out: empty
		{"The cache of the bug", "cache bug", true},
		{"alpha beta gamma delta epsilon", "alpha beta gamma delta zeta", false}, // 4/6
		{"alpha beta gamma delta", "alpha beta gamma delta epsilon", true},       // 4/5 = 0.8
		{"", "", false},
		{"alpha beta gamma delta", "alpha beta gamma", false},
	}
	for _, tt := range tests {
		if got := SimilarTitles(tt.a, tt.b); got != tt.want {
			t.Errorf("SimilarTitles(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSkipNearDuplicates(t *testing.T) {
	items := []Item{
		{F: Finding{Fingerprint: "low", Title: "Handler leaks goroutine on shutdown path", Severity: SevLow}},
		{F: Finding{Fingerprint: "high", Title: "Handler leaks goroutine on shutdown path!", Severity: SevHigh}},
		{F: Finding{Fingerprint: "other", Title: "Unrelated thing entirely", Severity: SevLow}},
		{F: Finding{Fingerprint: "open", Title: "Config parser drops comments quietly", Severity: SevLow}},
	}
	open := []TitledIssue{{Number: 7, Titles: []string{"x", "config parser drops comments quietly"}}, {Number: 0, Titles: []string{"Unrelated thing entirely"}}}
	kept, dups := SkipNearDuplicates(items, open)
	if len(kept) != 2 || kept[0].F.Fingerprint != "high" {
		t.Fatalf("kept = %v", kept)
	}
	want := map[string]int{"low": 0, "open": 7}
	if len(dups) != 2 {
		t.Fatalf("dups = %v", dups)
	}
	for _, d := range dups {
		if w, ok := want[d.Fingerprint]; !ok || w != d.Issue {
			t.Errorf("dup %+v", d)
		}
	}
	if k, d := SkipNearDuplicates(items, nil); len(k) != 3 || len(d) != 1 {
		t.Errorf("empty open: kept %d dups %d", len(k), len(d))
	}
}
