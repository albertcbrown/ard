package scanner

import (
	"sort"
	"strings"
)

// NearDupThreshold is the Jaccard similarity at which two titles with at
// least three tokens each are near-duplicates.
const NearDupThreshold = 0.8

// TitledIssue is an open issue with every title it carries (its own and its
// checklist lines').
type TitledIssue struct {
	Number int
	Titles []string
}

// NearDup records a skipped finding and the open issue (or, when Issue is 0,
// an earlier finding of the same run) it duplicates.
type NearDup struct {
	Fingerprint string
	Issue       int
}

var nearDupStop = map[string]bool{"a": true, "an": true, "and": true, "for": true, "in": true, "is": true, "of": true, "on": true, "the": true, "to": true, "with": true}

func titleTokens(s string) map[string]bool {
	b := []rune(strings.ToLower(s))
	for i, r := range b {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			b[i] = ' '
		}
	}
	set := map[string]bool{}
	for _, w := range strings.Fields(string(b)) {
		if len(w) > 1 && !nearDupStop[w] {
			set[w] = true
		}
	}
	return set
}

// SimilarTitles reports whether two titles are near-duplicates: equal
// non-empty token sets, or, when both have at least three tokens, a Jaccard
// similarity of NearDupThreshold or more.
func SimilarTitles(a, b string) bool {
	x, y := titleTokens(a), titleTokens(b)
	if len(x) == 0 || len(y) == 0 {
		return false
	}
	inter := 0
	for w := range x {
		if y[w] {
			inter++
		}
	}
	if inter == len(x) && inter == len(y) {
		return true
	}
	if len(x) < 3 || len(y) < 3 {
		return false
	}
	union := len(x) + len(y) - inter
	return float64(inter)/float64(union) >= NearDupThreshold
}

// SkipNearDuplicates drops items whose title is similar to an open issue's or
// to an earlier, more severe kept item of the same run. Items are compared in
// severity order. Skipped items are not recorded anywhere, so the next run
// offers them again.
func SkipNearDuplicates(items []Item, open []TitledIssue) (kept []Item, dups []NearDup) {
	sorted := append([]Item(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return lessItem(sorted[i], sorted[j]) })
	for _, it := range sorted {
		issue, found := -1, false
		for _, o := range open {
			if o.Number <= 0 {
				continue
			}
			for _, t := range o.Titles {
				if SimilarTitles(it.F.Title, t) {
					issue, found = o.Number, true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			for _, k := range kept {
				if SimilarTitles(it.F.Title, k.F.Title) {
					issue, found = 0, true
					break
				}
			}
		}
		if found {
			dups = append(dups, NearDup{Fingerprint: it.F.Fingerprint, Issue: issue})
			continue
		}
		kept = append(kept, it)
	}
	return kept, dups
}
