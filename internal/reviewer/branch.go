package reviewer

import (
	"strconv"
	"strings"
)

// parseWorkerBranch splits worker/<role>-<issue#>-<slug>. The issue is the
// first dash-separated segment after the first that is all ASCII digits, the
// role is the segments before it and at least one segment (the slug) must
// follow. Config rejects worker role names with an all-digit segment, so the
// split is exact: worker/v2-eng-7-2024-roadmap is role v2-eng, issue 7.
func parseWorkerBranch(ref string) (role string, issue int, ok bool) {
	rest, found := strings.CutPrefix(ref, "worker/")
	if !found {
		return "", 0, false
	}
	segs := strings.Split(rest, "-")
	for i := 1; i < len(segs)-1; i++ {
		if !digits(segs[i]) {
			continue
		}
		n := 0
		for _, c := range segs[i] {
			n = n*10 + int(c-'0')
			if n > 1<<30 {
				return "", 0, false
			}
		}
		if n == 0 || segs[0] == "" || segs[len(segs)-1] == "" {
			return "", 0, false
		}
		for _, s := range segs[:i] {
			if s == "" {
				return "", 0, false
			}
		}
		return strings.Join(segs[:i], "-"), n, true
	}
	return "", 0, false
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// localRef is where a PR's head branch is fetched to in the controller's
// clone. The executor resolves the pinned SHA, not this name.
func localRef(pr int) string { return "refs/ard/review/" + strconv.Itoa(pr) }
