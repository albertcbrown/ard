package cli

import (
	"slices"
	"testing"
)

func TestCommandPaths(t *testing.T) {
	got := CommandPaths()
	for _, p := range []string{"ard", "ard serve", "ard init", "ard config check", "ard labels sync", "ard rebuild-state", "ard version"} {
		if _, ok := got[p]; !ok {
			t.Errorf("missing command path %q", p)
		}
	}
	if !slices.Contains(got["ard serve"], "--loops") || !slices.Contains(got["ard serve"], "--dry-run") {
		t.Errorf("ard serve flags = %v", got["ard serve"])
	}
}
