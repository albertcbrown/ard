package merger

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/prgate"
)

type checkState int

const (
	checksGreen checkState = iota
	checksPending
	checksRed
)

func (s checkState) String() string {
	return [...]string{"green", "pending", "red"}[s]
}

// runState classifies one check run the way GitHub does: completed with
// success, neutral or skipped passes, any other completed conclusion fails,
// anything not completed is still going.
func runState(r github.CheckRun) checkState {
	if r.Status != "completed" {
		return checksPending
	}
	switch r.Conclusion {
	case "success", "neutral", "skipped":
		return checksGreen
	}
	return checksRed
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

// evalChecks judges the check runs of a head. With a required list, each
// required context is matched by name (and by app when the rule names an
// integration) and the latest matching run decides; a missing run is pending.
// With no list, there is nothing to go by: no runs at all is green once
// graceOver (CI had time to queue) and pending before, otherwise every latest
// run per name must be green. Red outranks pending. names are the failing (or
// waiting) contexts, sorted.
func evalChecks(required []github.RequiredCheck, runs []github.CheckRun, graceOver bool) (checkState, []string) {
	var red, pending []string
	if len(required) > 0 {
		for _, rc := range required {
			var best *github.CheckRun
			for i := range runs {
				r := &runs[i]
				if r.Name != rc.Context || (rc.IntegrationID != 0 && r.AppID != rc.IntegrationID) {
					continue
				}
				if best == nil || newer(*r, *best) {
					best = r
				}
			}
			switch {
			case best == nil:
				pending = append(pending, rc.Context)
			case runState(*best) == checksRed:
				red = append(red, rc.Context)
			case runState(*best) == checksPending:
				pending = append(pending, rc.Context)
			}
		}
	} else {
		if len(runs) == 0 {
			if graceOver {
				return checksGreen, nil
			}
			return checksPending, nil
		}
		latest := map[string]github.CheckRun{}
		for _, r := range runs {
			if cur, ok := latest[r.Name]; !ok || newer(r, cur) {
				latest[r.Name] = r
			}
		}
		for name, r := range latest {
			switch runState(r) {
			case checksRed:
				red = append(red, name)
			case checksPending:
				pending = append(pending, name)
			}
		}
	}
	sort.Strings(red)
	sort.Strings(pending)
	switch {
	case len(red) > 0:
		return checksRed, red
	case len(pending) > 0:
		return checksPending, pending
	}
	return checksGreen, nil
}

// behind reports whether main's changes since the merge base (changed, the
// paths that differ between the base and main's tip) include any outside
// .ard/<digits>/. Close-out commits to other issues' directories do not make a
// branch stale.
func behind(changed []string) bool {
	for _, p := range changed {
		if !prgate.IsIssuePath(p) {
			return true
		}
	}
	return false
}

// closingRef reports whether the first line of body is exactly
// "Closes #<issue>", the line worker.Body writes. The rest of the body is
// untrusted text and never read.
func closingRef(body string, issue int) bool {
	first, _, _ := strings.Cut(body, "\n")
	return strings.TrimSuffix(first, "\r") == "Closes #"+strconv.Itoa(issue)
}

// approvalCheck is what approved needs from the outside.
type approvalCheck struct {
	self       string // the App's login
	permission func(ctx context.Context, login string) (github.Permission, error)
	covers     func(ctx context.Context, reviewedAt string) (bool, error)
}

// approved reports whether a human with write access approved a commit that
// still covers the head. For each reviewer the last APPROVED, CHANGES_REQUESTED
// or DISMISSED review in submission order decides (a plain comment does not
// void an approval); only an APPROVED one counts. Reviews by the App or any
// bot are ignored. A permission or git error is returned: the caller fails
// closed and retries.
func approved(ctx context.Context, reviews []github.Review, c approvalCheck) (bool, error) {
	sorted := append([]github.Review(nil), reviews...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].SubmittedAt.Equal(sorted[j].SubmittedAt) {
			return sorted[i].SubmittedAt.Before(sorted[j].SubmittedAt)
		}
		return sorted[i].ID < sorted[j].ID
	})
	last := map[string]github.Review{}
	for _, r := range sorted {
		switch r.State {
		case "APPROVED", "CHANGES_REQUESTED", "DISMISSED":
			last[strings.ToLower(r.Author)] = r
		}
	}
	logins := make([]string, 0, len(last))
	for l := range last {
		logins = append(logins, l)
	}
	sort.Strings(logins)
	for _, login := range logins {
		r := last[login]
		if r.State != "APPROVED" || r.CommitSHA == "" || r.Author == "" ||
			strings.EqualFold(r.Author, c.self) || strings.HasSuffix(strings.ToLower(r.Author), "[bot]") {
			continue
		}
		ok, err := c.covers(ctx, r.CommitSHA)
		if err != nil {
			return false, fmt.Errorf("approval by %s: %w", r.Author, err)
		}
		if !ok {
			continue
		}
		perm, err := c.permission(ctx, r.Author)
		if err != nil {
			return false, fmt.Errorf("permission of %s: %w", r.Author, err)
		}
		if perm.AtLeastWrite() {
			return true, nil
		}
	}
	return false, nil
}
