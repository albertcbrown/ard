package prgate_test

import (
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/prgate"
	"github.com/albertcbrown/ard/internal/worker"
)

// An external test package: worker imports prgate through reviewfix.
func TestParseWorkerBranchRoundTrip(t *testing.T) {
	role, n, ok := prgate.ParseWorkerBranch(worker.BranchName("v2-eng", 7, strings.Repeat("long ", 30)))
	if !ok || role != "v2-eng" || n != 7 {
		t.Errorf("round trip: %q %d %v", role, n, ok)
	}
}
