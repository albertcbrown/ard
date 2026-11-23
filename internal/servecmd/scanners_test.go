package servecmd

import (
	"testing"

	"github.com/albertcbrown/ard/internal/github"
)

func TestRepoWorkDir(t *testing.T) {
	got, err := repoWorkDir("/h", github.Repo{Owner: "Foo", Name: "Bar"})
	if err != nil || got != "/h/.ard/work/foo/bar" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := repoWorkDir("/h", github.Repo{Owner: "..", Name: "x"}); err == nil {
		t.Error("invalid repo accepted")
	}
}

func TestScannerPipelineStatusIssueWired(t *testing.T) {
	w := &wiring{}
	p := scannerPipeline(w, nil, nil)
	if p.StatusIssue == nil {
		t.Fatal("StatusIssue not wired")
	}
	if got := p.StatusIssue(); got != 0 {
		t.Errorf("before startup: %d, want 0", got)
	}
	w.statusIssue.Store(42)
	if got := p.StatusIssue(); got != 42 {
		t.Errorf("after store: %d, want 42", got)
	}
}
