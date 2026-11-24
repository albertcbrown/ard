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
