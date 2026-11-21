//go:build integration

package github_test

import (
	"os"
	"testing"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/clienttest"
)

// TestIntegration runs the conformance suite against a real repository. It
// skips unless ARD_IT_REPO (owner/name) and ARD_GITHUB_* credentials are set.
// It creates issues and labels in that repo: point it at a scratch repository.
func TestIntegration(t *testing.T) {
	repoName := os.Getenv("ARD_IT_REPO")
	if repoName == "" {
		t.Skip("ARD_IT_REPO not set")
	}
	repo, err := github.ParseRepo(repoName)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := github.AuthFromEnv(os.Getenv, os.ReadFile)
	if err != nil {
		t.Skipf("no credentials: %v", err)
	}
	c, err := github.New(github.Config{Repo: repo, Auth: auth})
	if err != nil {
		t.Fatal(err)
	}
	// The merge-guard and PR-number cases need real branches and commits, so
	// no WithPR factory is supplied and they skip. The transfer case skips
	// because it needs a second repository to move an issue into.
	clienttest.Run(t, func(t *testing.T) github.Client { return c })
}
