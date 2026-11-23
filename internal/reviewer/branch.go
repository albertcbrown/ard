package reviewer

import (
	"strconv"

	"github.com/albertcbrown/ard/internal/prgate"
)

// parseWorkerBranch is prgate.ParseWorkerBranch, shared with the merger.
func parseWorkerBranch(ref string) (role string, issue int, ok bool) {
	return prgate.ParseWorkerBranch(ref)
}

// localRef is where a PR's head branch is fetched to in the controller's
// clone. The executor resolves the pinned SHA, not this name.
func localRef(pr int) string { return "refs/ard/review/" + strconv.Itoa(pr) }
