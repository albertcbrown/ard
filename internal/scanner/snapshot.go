package scanner

import (
	"sort"
	"time"

	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

// Aliases keep the pipeline's vocabulary short; the types live with the
// database that stores them.
type (
	Row         = scannerdb.Finding
	Suppression = scannerdb.Suppression
	Batch       = scannerdb.Batch
)

// Row statuses.
const (
	StatusOpen  = scannerdb.StatusOpen
	StatusGone  = scannerdb.StatusGone
	StatusFixed = scannerdb.StatusFixed
)

// Snapshot is one scanner's state in memory. A scanner's state is a few
// hundred rows, so every plan is a pure function of a Snapshot.
type Snapshot struct {
	Rows         map[string]Row         // by fingerprint
	Suppressions map[string]Suppression // by fingerprint; expired rows are kept
}

// NewSnapshot builds a Snapshot from stored rows.
func NewSnapshot(rows []Row, sups []Suppression) Snapshot {
	s := Snapshot{Rows: map[string]Row{}, Suppressions: map[string]Suppression{}}
	for _, r := range rows {
		s.Rows[r.Fingerprint] = r
	}
	for _, x := range sups {
		s.Suppressions[x.Fingerprint] = x
	}
	return s
}

// Apply mirrors scannerdb's Batch semantics in memory: deletes first, then
// upserts that keep the stored first_seen.
func (s *Snapshot) Apply(b Batch) {
	for _, fp := range b.DeleteFindings {
		delete(s.Rows, fp)
	}
	for _, fp := range b.DeleteSuppressions {
		delete(s.Suppressions, fp)
	}
	for _, r := range b.PutFindings {
		if old, ok := s.Rows[r.Fingerprint]; ok {
			r.FirstSeen = old.FirstSeen
		}
		s.Rows[r.Fingerprint] = r
	}
	for _, x := range b.PutSuppressions {
		s.Suppressions[x.Fingerprint] = x
	}
}

// Clone returns a deep copy.
func (s Snapshot) Clone() Snapshot {
	c := Snapshot{Rows: make(map[string]Row, len(s.Rows)), Suppressions: make(map[string]Suppression, len(s.Suppressions))}
	for k, v := range s.Rows {
		c.Rows[k] = v
	}
	for k, v := range s.Suppressions {
		c.Suppressions[k] = v
	}
	return c
}

// Sorted returns the rows and suppressions ordered by fingerprint.
func (s Snapshot) Sorted() ([]Row, []Suppression) {
	rows := make([]Row, 0, len(s.Rows))
	for _, r := range s.Rows {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Fingerprint < rows[j].Fingerprint })
	sups := make([]Suppression, 0, len(s.Suppressions))
	for _, x := range s.Suppressions {
		sups = append(sups, x)
	}
	sort.Slice(sups, func(i, j int) bool { return sups[i].Fingerprint < sups[j].Fingerprint })
	return rows, sups
}

// tracked reports whether r is listed on an issue that was open when last seen.
func tracked(r Row) bool { return r.Status == StatusOpen || r.Status == StatusGone }

// syntheticOpen returns the line counts of the dry-run issues (negative
// numbers) that still have an open or gone row. Dry-run issues never reach
// GitHub, so the database is their only record.
func (s Snapshot) syntheticOpen() map[int][]Row {
	out := map[int][]Row{}
	for _, r := range s.Rows {
		if r.Issue < 0 && tracked(r) {
			out[r.Issue] = append(out[r.Issue], r)
		}
	}
	return out
}

func utc(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }
