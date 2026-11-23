package scanner

import (
	"sort"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/github"
)

// IssueView is an open scanner issue as reconcile and grouping see it.
type IssueView struct {
	Number    int
	Title     string
	Labels    []string
	CreatedAt time.Time
	Body      string
	Parsed    Parsed
}

// FateKind says what happened to an issue that left the open list.
type FateKind int

// Fates of a tracked issue that is not in the open list.
const (
	FateOpen       FateKind = iota + 1 // still open, but no longer labelled for this scanner
	FateCompleted                      // closed as completed, or with no reason
	FateSuppressed                     // closed as not planned or as a duplicate
	FateGone                           // deleted or transferred
)

// Fate is the result of looking up one tracked issue.
type Fate struct {
	Kind     FateKind
	ClosedAt time.Time
	Reason   github.StateReason
}

// FateOf classifies a fetched issue. repo ("owner/name") detects a transfer:
// GitHub redirects a transferred issue's number, so the fetched issue's URL
// names another repository. An empty repo or URL skips that check.
func FateOf(is github.Issue, repo string) Fate {
	if repo != "" && is.URL != "" && !strings.Contains(strings.ToLower(is.URL), "/"+strings.ToLower(repo)+"/") {
		return Fate{Kind: FateGone}
	}
	if is.State != github.StateClosed {
		return Fate{Kind: FateOpen}
	}
	f := Fate{ClosedAt: time.Time{}, Reason: is.StateReason}
	if is.ClosedAt != nil {
		f.ClosedAt = *is.ClosedAt
	}
	switch is.StateReason {
	case github.ReasonNotPlanned, github.ReasonDuplicate:
		f.Kind = FateSuppressed
	default: // completed, reopened-then-closed with no reason, or nil
		f.Kind = FateCompleted
	}
	return f
}

// ReconcileResult is what Reconcile decided.
type ReconcileResult struct {
	Batch      Batch
	Duplicates []string // fingerprints listed on more than one open issue
}

// Reconcile brings snap in line with GitHub, which is the truth:
//
//   - every marker line on an open issue becomes an open or gone row on that
//     issue (this adopts rows a crash left unrecorded, and follows human moves
//     and ticks); a fingerprint on two open issues goes to the lowest number;
//   - a tracked row whose issue is open but whose line was deleted is dropped;
//   - a tracked row whose issue left the open list follows its fate: completed
//     becomes a fixed tombstone, not planned or duplicate becomes a suppression
//     until closed_at + suppressFor, deleted or transferred is dropped;
//   - an issue that is open again loses its suppressions.
//
// Rows on synthetic (negative) issues are never touched. Suppressions are
// derived from closed_at, not from now, so reconcile is idempotent and a
// rebuild reproduces it.
func Reconcile(snap Snapshot, open []IssueView, fates map[int]Fate, suppressFor time.Duration, now time.Time) ReconcileResult {
	var res ReconcileResult
	b := &res.Batch
	sorted := append([]IssueView(nil), open...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Number < sorted[j].Number })

	type listed struct {
		iss  IssueView
		line Line
	}
	onOpen := map[string]listed{}
	openNums := map[int]bool{}
	for _, iv := range sorted {
		openNums[iv.Number] = true
		for _, l := range iv.Parsed.Lines {
			if first, dup := onOpen[l.Fingerprint]; dup {
				if first.iss.Number != iv.Number {
					res.Duplicates = append(res.Duplicates, l.Fingerprint)
				}
				continue
			}
			onOpen[l.Fingerprint] = listed{iv, l}
		}
	}

	fps := make([]string, 0, len(onOpen))
	for fp := range onOpen {
		fps = append(fps, fp)
	}
	sort.Strings(fps)
	for _, fp := range fps {
		ls := onOpen[fp]
		want := Row{
			Fingerprint: fp, Issue: ls.iss.Number, Status: StatusOpen,
			Severity: string(ls.line.Severity), Group: ls.iss.Parsed.Group,
			FirstSeen: ls.iss.CreatedAt, LastSeen: ls.iss.CreatedAt,
		}
		if ls.line.Checked {
			want.Status = StatusGone
		}
		old, had := snap.Rows[fp]
		if had {
			want.FirstSeen, want.LastSeen, want.LastSHA = old.FirstSeen, old.LastSeen, old.LastSHA
			if old.Issue == want.Issue && old.Status == want.Status && old.Severity == want.Severity && old.Group == want.Group {
				continue
			}
		}
		if want.FirstSeen.IsZero() {
			want.FirstSeen, want.LastSeen = now, now
		}
		b.PutFindings = append(b.PutFindings, want)
	}

	rowFPs := make([]string, 0, len(snap.Rows))
	for fp := range snap.Rows {
		rowFPs = append(rowFPs, fp)
	}
	sort.Strings(rowFPs)
	for _, fp := range rowFPs {
		r := snap.Rows[fp]
		if _, ok := onOpen[fp]; ok || !tracked(r) || r.Issue <= 0 {
			continue
		}
		if openNums[r.Issue] { // the line was deleted from the body
			b.DeleteFindings = append(b.DeleteFindings, fp)
			continue
		}
		fate, ok := fates[r.Issue]
		if !ok {
			continue // not looked up (or the lookup failed): leave it for the next run
		}
		switch fate.Kind {
		case FateCompleted:
			r.Status = StatusFixed
			b.PutFindings = append(b.PutFindings, r)
		case FateSuppressed:
			b.DeleteFindings = append(b.DeleteFindings, fp)
			if old, ok := snap.Suppressions[fp]; ok && old.Issue > r.Issue {
				continue // a later issue's suppression wins
			}
			closed := fate.ClosedAt
			if closed.IsZero() {
				closed = now
			}
			b.PutSuppressions = append(b.PutSuppressions, Suppression{
				Fingerprint: fp, Until: closed.Add(suppressFor), Severity: r.Severity,
				Issue: r.Issue, Reason: string(fate.Reason),
			})
		case FateGone:
			b.DeleteFindings = append(b.DeleteFindings, fp)
		}
	}

	supFPs := make([]string, 0, len(snap.Suppressions))
	for fp := range snap.Suppressions {
		supFPs = append(supFPs, fp)
	}
	sort.Strings(supFPs)
	deleted := map[string]bool{}
	for _, fp := range supFPs {
		x := snap.Suppressions[fp]
		switch {
		case openNums[x.Issue]: // reopened
			deleted[fp] = true
		case x.Issue > 0 && fates[x.Issue].Kind == FateGone:
			deleted[fp] = true
		}
	}
	// A suppression replaced in this batch must not also be deleted.
	for _, x := range b.PutSuppressions {
		delete(deleted, x.Fingerprint)
	}
	for _, fp := range supFPs {
		if deleted[fp] {
			b.DeleteSuppressions = append(b.DeleteSuppressions, fp)
		}
	}
	return res
}
