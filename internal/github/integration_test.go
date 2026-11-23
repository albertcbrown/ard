//go:build integration

package github_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	gogh "github.com/google/go-github/v88/github"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/clienttest"
)

// TestIntegration runs the conformance suite against a real repository. It
// skips unless ARD_IT_REPO (owner/name) and ARD_GITHUB_* credentials are set.
// It creates issues and labels in that repo: point it at a scratch repository.
//
// Optional: ARD_IT_REQUIRED_CHECK names a status check the repo's rules require
// (RequiredChecksRead asserts it is reported). ARD_IT_RULES_UNAVAILABLE=1 is
// required on a free-plan private repository such as ard-scratch, whose rules
// API answers 403 "Upgrade to GitHub Pro"; without it RequiredChecksRead fails.
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
	// The transfer case skips because it needs a second repository to move an
	// issue into.
	rt, err := auth.Transport(http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	gh, err := gogh.NewClient(gogh.WithHTTPClient(&http.Client{Transport: rt, Timeout: time.Minute}), gogh.WithUserAgent("ard-it"))
	if err != nil {
		t.Fatal(err)
	}
	opts := []clienttest.Option{
		// Real GitHub's label and state filters lag a write by ~20 s.
		clienttest.WithEventualConsistency(60*time.Second, 3*time.Second),
		clienttest.WithPR(func(t *testing.T, c github.Client) (int, string) {
			return newScratchPR(t, c, gh, repo)
		}),
		clienttest.WithPin(func(t *testing.T, c github.Client, n int) {
			// Closing does not unpin, and a repo has only 3 pin slots, so
			// unpin through GraphQL (the client has no unpin), then close.
			unpinIssue(t, c, rt, n)
			closed := github.StateClosed
			if err := c.UpdateIssue(context.Background(), n, github.IssueUpdate{State: &closed}); err != nil {
				t.Logf("cleanup: close pinned issue %d: %v", n, err)
			}
		}),
	}
	if rc := os.Getenv("ARD_IT_REQUIRED_CHECK"); rc != "" {
		opts = append(opts, clienttest.WithRequiredCheck(rc))
	}
	// ard-scratch is private on a free plan: its rules API answers 403
	// "Upgrade to GitHub Pro", which RequiredChecks reports as unavailable.
	// Set ARD_IT_RULES_UNAVAILABLE=1 for such a repository.
	if os.Getenv("ARD_IT_RULES_UNAVAILABLE") != "" {
		opts = append(opts, clienttest.WithRulesUnavailable())
	}
	clienttest.Run(t, func(t *testing.T) github.Client { return c }, opts...)
}

// unpinIssue unpins issue n with the GraphQL unpinIssue mutation, logging any
// failure: it is cleanup, and a leftover pin needs only a manual unpin.
func unpinIssue(t *testing.T, c github.Client, rt http.RoundTripper, n int) {
	t.Helper()
	ctx := context.Background()
	is, err := c.GetIssue(ctx, n)
	if err != nil {
		t.Logf("cleanup: unpin issue %d: %v", n, err)
		return
	}
	body, err := json.Marshal(map[string]any{
		"query":     "mutation($id: ID!) { unpinIssue(input: {issueId: $id}) { clientMutationId } }",
		"variables": map[string]string{"id": is.NodeID},
	})
	if err != nil {
		t.Logf("cleanup: unpin issue %d: %v", n, err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.github.com/graphql", bytes.NewReader(body))
	if err != nil {
		t.Logf("cleanup: unpin issue %d: %v", n, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Transport: rt, Timeout: time.Minute}).Do(req)
	if err != nil {
		t.Logf("cleanup: unpin issue %d: %v", n, err)
		return
	}
	defer resp.Body.Close()
	var out struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK || len(out.Errors) > 0 {
		t.Logf("cleanup: unpin issue %d: status %d, decode err %v, errors %v", n, resp.StatusCode, err, out.Errors)
	}
}

// newScratchPR opens a real, mergeable PR that adds one uniquely named file on
// top of the default branch, and registers cleanup that closes it (if still
// open) and deletes its branch.
func newScratchPR(t *testing.T, c github.Client, gh *gogh.Client, repo github.Repo) (int, string) {
	t.Helper()
	ctx := context.Background()
	uniq := fmt.Sprintf("%d", time.Now().UnixNano())
	branch := "ct-" + uniq
	path := "ct/" + uniq + ".txt"

	r, _, err := gh.Repositories.Get(ctx, repo.Owner, repo.Name)
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	base := r.GetDefaultBranch()
	ref, _, err := gh.Git.GetRef(ctx, repo.Owner, repo.Name, "heads/"+base)
	if err != nil {
		t.Fatalf("get ref %s: %v", base, err)
	}
	parent, _, err := gh.Git.GetCommit(ctx, repo.Owner, repo.Name, ref.Object.GetSHA())
	if err != nil {
		t.Fatalf("get commit: %v", err)
	}
	tree, _, err := gh.Git.CreateTree(ctx, repo.Owner, repo.Name, parent.Tree.GetSHA(), []*gogh.TreeEntry{{
		Path: gogh.Ptr(path), Mode: gogh.Ptr("100644"), Type: gogh.Ptr("blob"),
		Content: gogh.Ptr("conformance suite " + uniq + "\n"),
	}})
	if err != nil {
		t.Fatalf("create tree: %v", err)
	}
	commit, _, err := gh.Git.CreateCommit(ctx, repo.Owner, repo.Name, gogh.Commit{
		Message: gogh.Ptr("conformance suite " + uniq),
		Tree:    tree,
		Parents: []*gogh.Commit{{SHA: parent.SHA}},
	}, nil)
	if err != nil {
		t.Fatalf("create commit: %v", err)
	}
	if _, _, err := gh.Git.CreateRef(ctx, repo.Owner, repo.Name, gogh.CreateRef{Ref: "refs/heads/" + branch, SHA: commit.GetSHA()}); err != nil {
		t.Fatalf("create ref: %v", err)
	}
	var prN int // set once the PR exists; 0 makes the close a harmless 404
	t.Cleanup(func() {
		bg := context.Background()
		if _, _, err := gh.PullRequests.Edit(bg, repo.Owner, repo.Name, prN, &gogh.PullRequest{State: gogh.Ptr("closed")}); err != nil && !isNotFoundOrMerged(err) {
			t.Logf("cleanup: close PR for %s: %v", branch, err)
		}
		if _, err := gh.Git.DeleteRef(bg, repo.Owner, repo.Name, "heads/"+branch); err != nil && !isNotFoundOrMerged(err) {
			t.Logf("cleanup: delete branch %s: %v", branch, err)
		}
	})
	pr, err := c.CreatePR(ctx, github.NewPR{Title: "conformance suite " + uniq, Body: "Created by the ard conformance suite; safe to close.", Head: branch, Base: base})
	if err != nil {
		t.Fatalf("create PR: %v", err)
	}
	prN = pr.Number
	return pr.Number, commit.GetSHA()
}

// isNotFoundOrMerged reports errors cleanup ignores: the branch or PR is gone
// (a merged PR's branch may be deleted by the repo) or the PR cannot change.
func isNotFoundOrMerged(err error) bool {
	var ge *gogh.ErrorResponse
	if errors.As(err, &ge) && ge.Response != nil {
		switch ge.Response.StatusCode {
		case http.StatusNotFound, http.StatusUnprocessableEntity:
			return true
		}
	}
	return false
}
