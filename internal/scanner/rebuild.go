package scanner

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

// Rebuild reconstructs a scanner's state from its issues on GitHub (open and
// closed), using only the hidden markers. For each fingerprint:
//
//   - if it is listed on an open issue, the lowest-numbered one wins and the
//     row is open or gone (by its checkbox);
//   - otherwise the highest-numbered closed issue that lists it decides: a
//     completed close is a fixed tombstone, a not-planned or duplicate close
//     leaves no row;
//   - independently, the highest-numbered issue closed as not planned or as a
//     duplicate gives a suppression until closed_at + stale.suppress_for, kept
//     even when it has expired, as the live database keeps it.
//
// first_seen and last_seen are the issue's creation time; last_sha and the run
// history are not recoverable and are not part of Equal.
func Rebuild(ctx context.Context, gh Reader, cfg *config.Resolved, name string, now time.Time) (Snapshot, error) {
	issues, err := gh.ListIssues(ctx, github.ListIssuesOpts{State: github.StateAll, Labels: []string{labels.Scanner(name)}})
	if err != nil {
		return Snapshot{}, fmt.Errorf("list %s issues: %w", name, err)
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	suppressFor := time.Duration(cfg.Stale.SuppressFor)

	snap := NewSnapshot(nil, nil)
	type pick struct {
		row    Row
		isOpen bool
	}
	rows := map[string]pick{}
	for _, is := range issues { // ascending: later closed issues overwrite earlier ones
		if is.IsPR {
			continue
		}
		p := ParseBody(is.Body)
		fate := FateOf(is, "")
		for _, l := range p.Lines {
			row := Row{
				Fingerprint: l.Fingerprint, Issue: is.Number, Severity: string(l.Severity), Group: p.Group,
				FirstSeen: is.CreatedAt, LastSeen: is.CreatedAt,
			}
			switch fate.Kind {
			case FateOpen:
				row.Status = StatusOpen
				if l.Checked {
					row.Status = StatusGone
				}
				if cur, ok := rows[l.Fingerprint]; !ok || !cur.isOpen {
					rows[l.Fingerprint] = pick{row, true} // ascending order: the lowest open issue wins
				}
			case FateCompleted:
				row.Status = StatusFixed
				if cur, ok := rows[l.Fingerprint]; !ok || !cur.isOpen {
					rows[l.Fingerprint] = pick{row, false}
				}
			case FateSuppressed:
				if cur, ok := rows[l.Fingerprint]; !ok || !cur.isOpen {
					delete(rows, l.Fingerprint) // the latest closed issue says: no row
				}
				closed := fate.ClosedAt
				if closed.IsZero() {
					closed = now
				}
				snap.Suppressions[l.Fingerprint] = Suppression{
					Fingerprint: l.Fingerprint, Until: closed.Add(suppressFor), Severity: string(l.Severity),
					Issue: is.Number, Reason: string(fate.Reason),
				}
			}
		}
	}
	for fp, pk := range rows {
		snap.Rows[fp] = pk.row
	}
	return snap, nil
}

// RebuildReport lists what a rebuild would change.
type RebuildReport struct {
	Added, Removed, Changed []string // human-readable, sorted
}

// Empty reports whether the two snapshots are equal.
func (r RebuildReport) Empty() bool { return len(r.Added)+len(r.Removed)+len(r.Changed) == 0 }

func rowKey(r Row) string {
	return fmt.Sprintf("#%d %s sev=%s group=%q", r.Issue, r.Status, r.Severity, r.Group)
}

func supKey(s Suppression) string {
	return fmt.Sprintf("#%d until=%s sev=%s reason=%s", s.Issue, utc(s.Until).Format(time.RFC3339), s.Severity, s.Reason)
}

// DiffSnapshots compares two snapshots on what a rebuild can recover:
// (fingerprint, issue, status, severity, group) for rows and
// (fingerprint, issue, until, severity, reason) for suppressions. Timestamps,
// last_sha and runs are excluded.
func DiffSnapshots(old, fresh Snapshot) RebuildReport {
	var rep RebuildReport
	for fp, r := range fresh.Rows {
		o, ok := old.Rows[fp]
		switch {
		case !ok:
			rep.Added = append(rep.Added, "finding "+fp+" "+rowKey(r))
		case rowKey(o) != rowKey(r):
			rep.Changed = append(rep.Changed, fmt.Sprintf("finding %s: %s -> %s", fp, rowKey(o), rowKey(r)))
		}
	}
	for fp, o := range old.Rows {
		if _, ok := fresh.Rows[fp]; !ok {
			rep.Removed = append(rep.Removed, "finding "+fp+" "+rowKey(o))
		}
	}
	for fp, s := range fresh.Suppressions {
		o, ok := old.Suppressions[fp]
		switch {
		case !ok:
			rep.Added = append(rep.Added, "suppression "+fp+" "+supKey(s))
		case supKey(o) != supKey(s):
			rep.Changed = append(rep.Changed, fmt.Sprintf("suppression %s: %s -> %s", fp, supKey(o), supKey(s)))
		}
	}
	for fp, o := range old.Suppressions {
		if _, ok := fresh.Suppressions[fp]; !ok {
			rep.Removed = append(rep.Removed, "suppression "+fp+" "+supKey(o))
		}
	}
	sort.Strings(rep.Added)
	sort.Strings(rep.Removed)
	sort.Strings(rep.Changed)
	return rep
}
