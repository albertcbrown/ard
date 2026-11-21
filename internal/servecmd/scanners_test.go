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
