package scanner

import (
	"sort"
	"time"
)

// Regression is a finding that came back after its issue was completed.
type Regression struct {
	Finding Finding
	Of      int // the old issue
}

// Plan is the result of Diff.
type Plan struct {
	New        []Finding    // unseen, or re-raised past a suppression
	Regress    []Regression // returned after a completed close
	Seen       []Finding    // already listed on an open issue
	Resurface  []Finding    // listed checked, reported again: uncheck (autoclose only)
	Gone       []string     // listed unchecked, no longer reported: check (autoclose only)
	Suppressed []Finding    // skipped: closed as not planned and not worse
}

// Diff compares a scan with the snapshot, per fingerprint:
//
//	no row, or suppression expired          found -> New
//	suppression active, severity not higher found -> Suppressed
//	suppression active, severity higher     found -> New (the suppression lifts)
//	fixed                                   found -> Regression of the old issue
//	open                                    found -> Seen; not found -> Gone
//	gone                                    found -> Resurface
//
// Gone and Resurface are produced only for autoclose scanners: a scanner that
// does not autoclose never edits or closes an issue for absence. found must
// have unique fingerprints (DecodeFindings guarantees it).
func Diff(snap Snapshot, found []Finding, autoclose bool, now time.Time) Plan {
	var p Plan
	seen := make(map[string]bool, len(found))
	for _, f := range found {
		seen[f.Fingerprint] = true
		row, has := snap.Rows[f.Fingerprint]
		switch {
		case has && row.Status == StatusFixed:
			p.Regress = append(p.Regress, Regression{Finding: f, Of: row.Issue})
		case has && row.Status == StatusOpen:
			p.Seen = append(p.Seen, f)
		case has && row.Status == StatusGone:
			if autoclose {
				p.Resurface = append(p.Resurface, f)
			} else {
				p.Seen = append(p.Seen, f)
			}
		default:
			sup, ok := snap.Suppressions[f.Fingerprint]
			switch {
			case !ok || !sup.Until.After(now):
				p.New = append(p.New, f)
			case f.Severity.Rank() > Severity(sup.Severity).Rank():
				p.New = append(p.New, f)
			default:
				p.Suppressed = append(p.Suppressed, f)
			}
		}
	}
	if autoclose {
		for fp, row := range snap.Rows {
			if row.Status == StatusOpen && !seen[fp] {
				p.Gone = append(p.Gone, fp)
			}
		}
		sort.Strings(p.Gone)
	}
	return p
}
