// Package prgate decides whether a worker PR's requested reviews have passed
// at its current head. The reviewer loop (#194, mark ready) and the merger
// (#46) share it, so the two can never disagree about "reviewed".
//
// The rule has three parts: the label rule (Labels), the record of which head
// each review saw (ReviewedHeads) and the head comparison (Covers). All are
// pure or read-only, and every git error fails closed.
package prgate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

// Reasons a Verdict is not OK.
const (
	ReasonNoReviews = "no_reviews"
	ReasonPending   = "reviews_pending"
	ReasonChanges   = "reviews_changes"
	ReasonRunning   = "reviews_running"
	ReasonBlocked   = "blocked_label"
	ReasonStale     = "reviews_stale"
)

// Verdict is the outcome of the label rule.
type Verdict struct {
	OK     bool
	Reason string   // one of the Reason* constants; "" when OK
	Roles  []string // requested review roles, in the order of the roles argument
}

// blocking are the PR labels that stop every automatic step.
var blocking = []string{labels.NeedsHuman, labels.Hold, labels.TestsFailed, labels.ExecutorFailed}

// Blocked reports whether the PR carries a label that stops automation.
func Blocked(prLabels []string) bool { return HasAny(prLabels, blocking) }

// Labels applies the label rule: at least one ard:review:<role> request among
// roles, each requested role has :done and neither :changes nor :running, and
// the PR carries none of needs-human, hold, tests:failed, executor:failed.
// Pure.
func Labels(prLabels []string, roles []string) Verdict {
	if Blocked(prLabels) {
		return Verdict{Reason: ReasonBlocked}
	}
	var requested []string
	for _, role := range roles {
		if !HasLabel(prLabels, labels.Request(labels.StageReview, role)) {
			continue
		}
		switch {
		case HasLabel(prLabels, labels.Changes(role)):
			return Verdict{Reason: ReasonChanges, Roles: requested}
		case HasLabel(prLabels, labels.Running(labels.StageReview, role)):
			return Verdict{Reason: ReasonRunning, Roles: requested}
		case !HasLabel(prLabels, labels.Done(labels.StageReview, role)):
			return Verdict{Reason: ReasonPending, Roles: requested}
		}
		requested = append(requested, role)
	}
	if len(requested) == 0 {
		return Verdict{Reason: ReasonNoReviews}
	}
	return Verdict{OK: true, Roles: requested}
}

// HasLabel reports whether have holds want, ignoring case as GitHub does.
func HasLabel(have []string, want string) bool {
	for _, h := range have {
		if strings.EqualFold(h, want) {
			return true
		}
	}
	return false
}

// HasAny reports whether have holds any of want.
func HasAny(have, want []string) bool {
	for _, w := range want {
		if HasLabel(have, w) {
			return true
		}
	}
	return false
}

// Git is the part of *gitx.Git Covers uses.
type Git interface {
	CodeChanged(ctx context.Context, repoDir, from, to string, exclude ...string) ([]string, error)
}

// Covers reports whether a review (or approval) given at commit reviewed still
// covers head, and the paths that break the cover. base "" is the plain rule:
// the head is covered when no path outside .ard/<issue>/ (the close-out
// directory of the PR's own issue) changed between the two. With base = merge-base(main, head), a changed path is also tolerated
// when head has the base's version of it, because it came from main (an
// update-branch merge). A path both the PR and main changed since the review
// is not covered. Any git error fails closed: the error is returned and the
// head is not covered.
func Covers(ctx context.Context, g Git, repoDir, reviewed, head, base string, issue int) (bool, []string, error) {
	if reviewed == "" || head == "" {
		return false, nil, nil
	}
	if reviewed == head {
		return true, nil, nil
	}
	since, err := g.CodeChanged(ctx, repoDir, reviewed, head)
	if err != nil {
		return false, nil, err
	}
	since = dropIssuePaths(since, issue)
	if len(since) == 0 {
		return true, nil, nil
	}
	if base == "" {
		return false, since, nil
	}
	own, err := g.CodeChanged(ctx, repoDir, base, head)
	if err != nil {
		return false, nil, err
	}
	ownSet := make(map[string]bool, len(own))
	for _, p := range own {
		ownSet[p] = true
	}
	var broken []string
	for _, p := range since {
		if ownSet[p] {
			broken = append(broken, p)
		}
	}
	return len(broken) == 0, broken, nil
}

func dropIssuePaths(paths []string, issue int) []string {
	var out []string
	for _, p := range paths {
		if !strings.HasPrefix(p, fmt.Sprintf(".ard/%d/", issue)) {
			out = append(out, p)
		}
	}
	return out
}

// IsIssuePath reports whether p is under .ard/<digits>/, the one place
// close-out commits land.
func IsIssuePath(p string) bool {
	rest, ok := strings.CutPrefix(p, ".ard/")
	if !ok {
		return false
	}
	dir, _, found := strings.Cut(rest, "/")
	return found && isDigits(dir)
}

// Store is the part of *state.Store ReviewedHeads uses.
type Store interface {
	Events(ctx context.Context, f state.EventFilter) ([]state.Event, error)
}

// ReviewedHeads returns, per role, the head SHA recorded by that role's newest
// review claim of the issue. A role without a record is absent.
func ReviewedHeads(ctx context.Context, s Store, issue int, roles []string) (map[string]string, error) {
	evs, err := s.Events(ctx, state.EventFilter{Kind: state.EventClaim, Issue: issue, Stage: "review"})
	if err != nil {
		return nil, fmt.Errorf("read review claims: %w", err)
	}
	want := map[string]bool{}
	for _, r := range roles {
		want[r] = true
	}
	out := map[string]string{}
	for _, e := range evs { // ascending: later claims overwrite
		if e.Issue != issue || e.Stage != "review" || !want[e.Role] {
			continue
		}
		var data struct {
			Head string `json:"head"`
		}
		if json.Unmarshal(e.Data, &data) == nil && data.Head != "" {
			out[e.Role] = data.Head
		}
	}
	return out, nil
}
