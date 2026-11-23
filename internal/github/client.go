package github

import (
	"context"
	"time"
)

// Reader is the read side of the GitHub client. Hand this (or a narrower
// interface declared at the consumer) to everything except the effect layer.
type Reader interface {
	Identity(ctx context.Context) (Identity, error) // cached after first success
	GetIssue(ctx context.Context, n int) (Issue, error)
	ListIssues(ctx context.Context, o ListIssuesOpts) ([]Issue, error)
	SearchIssues(ctx context.Context, q string, o SearchOpts) ([]Issue, error) // client prepends "repo:owner/name "
	ListComments(ctx context.Context, n int, since time.Time) ([]Comment, error)
	ListTimeline(ctx context.Context, n int) ([]TimelineEvent, error) // oldest first, all pages
	ListLabels(ctx context.Context) ([]Label, error)                  // all pages
	GetPermission(ctx context.Context, login string) (Permission, error)
	GetPR(ctx context.Context, n int) (PullRequest, error)
	ListPRs(ctx context.Context, o ListPRsOpts) ([]PullRequest, error)
	ListPRFiles(ctx context.Context, n int) ([]PRFile, error)
	ListReviewComments(ctx context.Context, n int) ([]ReviewComment, error)
	ListReviews(ctx context.Context, n int) ([]Review, error) // all pages, oldest first
	// RequiredChecks reads the required status checks of the rules that apply
	// to branch. See RequiredChecksResult for the "no rules API" result versus
	// an error.
	RequiredChecks(ctx context.Context, branch string) (RequiredChecksResult, error)
	ListCheckRuns(ctx context.Context, sha string) ([]CheckRun, error) // filter=latest, all pages
}

// Writer is the write side. Only the effect layer (#13) may hold one.
type Writer interface {
	CreateIssue(ctx context.Context, in NewIssue) (Issue, error)
	UpdateIssue(ctx context.Context, n int, u IssueUpdate) error // title, body, close with reason, reopen
	PinIssue(ctx context.Context, n int) error                   // GraphQL pinIssue
	AddLabels(ctx context.Context, n int, labels ...string) error
	RemoveLabel(ctx context.Context, n int, label string) error // nil when the label is absent (404); ErrNotFound if the issue moved
	CreateLabel(ctx context.Context, l Label) error             // ErrConflict if it exists
	UpdateLabel(ctx context.Context, name string, l Label) error
	CreateComment(ctx context.Context, n int, body string) (Comment, error)
	CreatePR(ctx context.Context, in NewPR) (PullRequest, error)
	UpdatePR(ctx context.Context, n int, u PRUpdate) error
	MarkReady(ctx context.Context, n int) error                             // GraphQL markPullRequestReadyForReview
	CreateReview(ctx context.Context, n int, in ReviewInput) (int64, error) // returns the review id
	UpdateBranch(ctx context.Context, n int, expectedHeadSHA string) error
	MergePR(ctx context.Context, n int, in MergeInput) (MergeResult, error)
	DeleteBranch(ctx context.Context, branch string) error // nil if already gone
}

// Client is the full surface.
type Client interface {
	Reader
	Writer
}
