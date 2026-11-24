package prgate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/albertcbrown/ard/internal/github"
)

// CheckState is the verdict on a head's checks.
type CheckState int

const (
	ChecksGreen CheckState = iota
	ChecksPending
	ChecksRed
)

func (s CheckState) String() string {
	return [...]string{"green", "pending", "red"}[s]
}

// runState classifies one check run the way GitHub does: completed with
// success, neutral or skipped passes, any other completed conclusion fails,
// anything not completed is still going.
func runState(r github.CheckRun) CheckState {
	if r.Status != "completed" {
		return ChecksPending
	}
	switch r.Conclusion {
	case "success", "neutral", "skipped":
		return ChecksGreen
	}
	return ChecksRed
}

// newer reports whether a is a later run than b. A run that has not completed
// is the newest (a rerun in flight must hide the old result, so the pending
// state wins); among completed runs the later CompletedAt wins, then the
// higher ID.
func newer(a, b github.CheckRun) bool {
	ac, bc := a.Status == "completed", b.Status == "completed"
	switch {
	case ac != bc:
		return !ac
	case !ac:
		return a.ID > b.ID
	case !a.CompletedAt.Equal(b.CompletedAt):
		return a.CompletedAt.After(b.CompletedAt)
	}
	return a.ID > b.ID
}

// statusState maps a commit status state: success is green, pending is
// pending, failure, error (and anything unknown) are red.
func statusState(s github.CommitStatus) CheckState {
	switch s.State {
	case "success":
		return ChecksGreen
	case "pending":
		return ChecksPending
	}
	return ChecksRed
}

// newerStatus reports whether a is a later status than b.
func newerStatus(a, b github.CommitStatus) bool { return a.UpdatedAt.After(b.UpdatedAt) }

// combine decides one context that may have both a check run and a commit
// status. The newer wins (an unfinished run is the newest); on a tie the worse
// state wins, so red from either side never counts as green.
func combine(run *github.CheckRun, st *github.CommitStatus) CheckState {
	switch {
	case run == nil && st == nil:
		return ChecksPending
	case st == nil:
		return runState(*run)
	case run == nil:
		return statusState(*st)
	}
	rs, ss := runState(*run), statusState(*st)
	switch {
	case run.Status != "completed":
		return rs
	case st.UpdatedAt.After(run.CompletedAt):
		return ss
	case run.CompletedAt.After(st.UpdatedAt):
		return rs
	}
	return max(rs, ss)
}

// EvalChecks judges the check runs and commit statuses of a head. With a
// required list, each required context is matched by name (when the rule names an integration, a check run must come from that app and
// a status must have a resolved AppID equal to it) and the latest run and latest status decide, the newer one
// winning; a context with neither is pending. With no list, there is nothing
// to go by: no runs and no statuses at all is green once graceOver (CI had
// time to queue) and pending before, otherwise every latest run and status per
// name must be green. Red outranks pending. names are the failing (or
// waiting) contexts, sorted.
func EvalChecks(required []github.RequiredCheck, runs []github.CheckRun, statuses []github.CommitStatus, graceOver bool) (CheckState, []string) {
	latestRun := func(name string, app int64) *github.CheckRun {
		var best *github.CheckRun
		for i := range runs {
			r := &runs[i]
			if r.Name != name || (app != 0 && r.AppID != app) {
				continue
			}
			if best == nil || newer(*r, *best) {
				best = r
			}
		}
		return best
	}
	latestStatus := func(name string, app int64) *github.CommitStatus {
		var best *github.CommitStatus
		for i := range statuses {
			s := &statuses[i]
			if s.Context == name && (app == 0 || s.AppID == app) && (best == nil || newerStatus(*s, *best)) {
				best = s
			}
		}
		return best
	}
	var red, pending []string
	judge := func(name string, app int64) {
		switch combine(latestRun(name, app), latestStatus(name, app)) {
		case ChecksRed:
			red = append(red, name)
		case ChecksPending:
			pending = append(pending, name)
		}
	}
	if len(required) > 0 {
		for _, rc := range required {
			judge(rc.Context, rc.IntegrationID)
		}
	} else {
		if len(runs) == 0 && len(statuses) == 0 {
			if graceOver {
				return ChecksGreen, nil
			}
			return ChecksPending, nil
		}
		names := map[string]bool{}
		for _, r := range runs {
			names[r.Name] = true
		}
		for _, s := range statuses {
			names[s.Context] = true
		}
		for name := range names {
			judge(name, 0)
		}
	}
	sort.Strings(red)
	sort.Strings(pending)
	switch {
	case len(red) > 0:
		return ChecksRed, red
	case len(pending) > 0:
		return ChecksPending, pending
	}
	return ChecksGreen, nil
}

// ResolveApps fills in AppID on the statuses that satisfy a rule pinned to an
// integration: a status posted by an App ("<slug>[bot]") gets that App's id
// from lookup (callers cache it); a status from a user stays 0 and so never
// satisfies a pinned rule. With no pinned rule lookup is never called. A lookup
// error is returned and the caller fails closed.
func ResolveApps(ctx context.Context, required []github.RequiredCheck, statuses []github.CommitStatus, lookup func(ctx context.Context, slug string) (int64, error)) error {
	pinned := map[string]bool{}
	for _, rc := range required {
		if rc.IntegrationID != 0 {
			pinned[rc.Context] = true
		}
	}
	if len(pinned) == 0 {
		return nil
	}
	for i := range statuses {
		s := &statuses[i]
		slug, ok := strings.CutSuffix(s.Creator, "[bot]")
		if !pinned[s.Context] || !ok || slug == "" {
			continue
		}
		id, err := lookup(ctx, slug)
		if err != nil {
			return fmt.Errorf("resolve app %s: %w", slug, err)
		}
		s.AppID = id
	}
	return nil
}
