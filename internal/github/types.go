package github

import (
	"strconv"
	"time"
)

// Repo identifies a GitHub repository.
type Repo struct{ Owner, Name string }

// String returns "owner/name".
func (r Repo) String() string { return r.Owner + "/" + r.Name }

// State is the open/closed state of an issue or PR. StateAll is valid only as
// a list filter.
type State string

const (
	StateOpen   State = "open"
	StateClosed State = "closed"
	StateAll    State = "all"
)

// StateReason says why an issue is closed (or reopened).
type StateReason string

const (
	ReasonCompleted  StateReason = "completed"
	ReasonNotPlanned StateReason = "not_planned"
	ReasonDuplicate  StateReason = "duplicate"
	ReasonReopened   StateReason = "reopened"
)

// Issue is a GitHub issue (or, when IsPR, the issue view of a PR).
type Issue struct {
	Number      int
	NodeID      string // for GraphQL (PinIssue)
	Title, Body string // Body is untrusted
	State       State
	StateReason StateReason
	Author      string // login
	Labels      []string
	IsPR        bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ClosedAt    *time.Time
	URL         string
}

// Label is a repository label. Color is six hex digits without '#'.
type Label struct{ Name, Color, Description string }

// Comment is an issue or PR conversation comment. Body is untrusted.
type Comment struct {
	ID        int64
	Author    string
	Body      string
	CreatedAt time.Time
	URL       string
}

// PullRequest is a pull request.
type PullRequest struct {
	Number         int
	NodeID         string
	Title, Body    string
	State          State
	Draft, Merged  bool
	Author         string
	Labels         []string
	HeadRef        string
	HeadSHA        string
	BaseRef        string
	Mergeable      *bool  // nil while GitHub is still computing
	MergeableState string // clean|behind|blocked|dirty|unstable|draft|unknown
	URL            string
}

// PRFile is one file changed by a PR.
type PRFile struct {
	Path, PreviousPath, Status string
	Additions, Deletions       int
}

// InlineComment is a line comment in a review. Side defaults to RIGHT.
type InlineComment struct {
	Path string
	Line int
	Side string
	Body string
}

// ReviewInput is a review; it is always posted with event COMMENT.
type ReviewInput struct {
	CommitSHA, Body string
	Comments        []InlineComment
}

// ReviewComment is an inline review comment on a PR.
type ReviewComment struct {
	ID        int64
	Author    string
	Path      string
	Line      int
	Body      string
	CommitSHA string
	CreatedAt time.Time
}

// Review is a submitted pull request review. Body is untrusted.
type Review struct {
	ID          int64
	Author      string
	Body        string
	State       string // COMMENTED, APPROVED, CHANGES_REQUESTED, DISMISSED
	CommitSHA   string
	SubmittedAt time.Time
}

// IssueRef points at another issue or PR.
type IssueRef struct {
	Number int
	IsPR   bool
	State  State
}

// TimelineEvent is one entry of an issue timeline.
type TimelineEvent struct {
	Event     string // labeled, unlabeled, commented, cross-referenced, ...
	Actor     string // login; "" when GitHub omits it
	CreatedAt time.Time
	Label     string    // labeled/unlabeled
	Body      string    // commented (untrusted)
	Source    *IssueRef // cross-referenced
}

// Permission is a user's collaborator permission on the repo.
type Permission struct {
	Level string // admin|write|read|none
	Role  string // admin|maintain|write|triage|read|""
}

// AtLeastTriage reports whether the user can triage issues (role triage or
// above).
func (p Permission) AtLeastTriage() bool {
	switch p.Role {
	case "admin", "maintain", "write", "triage":
		return true
	case "":
		return p.Level == "admin" || p.Level == "write"
	}
	return false
}

// AtLeastWrite reports whether the user can push to and merge on the repo
// (role write, maintain or admin). A triage user cannot.
func (p Permission) AtLeastWrite() bool {
	switch p.Role {
	case "admin", "maintain", "write":
		return true
	case "":
		return p.Level == "admin" || p.Level == "write"
	}
	return false
}

// AuthKind says how the client authenticates.
type AuthKind string

const (
	AuthApp   AuthKind = "app"
	AuthToken AuthKind = "token"
)

// Identity is the authenticated actor.
type Identity struct {
	Login string
	ID    int64
	Kind  AuthKind
}

// NoreplyEmail is the commit email GitHub associates with this identity.
func (i Identity) NoreplyEmail() string {
	return strconv.FormatInt(i.ID, 10) + "+" + i.Login + "@users.noreply.github.com"
}

// ListIssuesOpts filters ListIssues.
type ListIssuesOpts struct {
	State      State     // default StateOpen
	Labels     []string  // AND
	Since      time.Time // updated at or after; zero = no filter
	IncludePRs bool      // default false
	Max        int       // 0 = all pages
}

// SearchOpts shapes SearchIssues.
type SearchOpts struct {
	Sort  string // "created"|"updated"|""
	Order string // "asc"|"desc"
	Max   int    // default 100, hard cap 1000
}

// ListPRsOpts filters ListPRs.
type ListPRsOpts struct {
	State State
	Head  string // branch name; the client prefixes the owner
	Base  string
}

// NewIssue creates an issue.
type NewIssue struct {
	Title, Body string
	Labels      []string
}

// IssueUpdate changes an issue; nil fields are left alone.
type IssueUpdate struct {
	Title, Body *string
	State       *State
	StateReason *StateReason
}

// NewPR creates a pull request.
type NewPR struct {
	Title, Body, Head, Base string
	Draft                   bool
}

// PRUpdate changes a PR; nil fields are left alone.
type PRUpdate struct{ Title, Body *string }

// MergeInput is a squash merge. SHA is required: it is the head guard.
type MergeInput struct{ SHA, Title, Message string }

// MergeResult is the outcome of a merge.
type MergeResult struct{ SHA string }

// RequiredCheck is one status check a ruleset requires on a branch.
type RequiredCheck struct {
	Context       string
	IntegrationID int64 // 0: any source may report it
}

// RequiredChecksResult is what the rules API says about a branch.
//
// Available is false when the repository has no rules API to ask: a free-plan
// private repository (HTTP 403 "Upgrade to GitHub Pro or make this repository
// public") or a 404. That is a normal answer meaning "no required checks", not
// a failure. Available is true when the rules were read; Checks may then be
// empty (no required_status_checks rule applies). Any other failure (5xx,
// network, timeout, a permissions 403, an unexpected status) is returned as an
// ordinary error and callers must treat it as "rules could not be read".
type RequiredChecksResult struct {
	Available bool
	Checks    []RequiredCheck // union over every required_status_checks rule, deduped, sorted
	// Unevaluated names rule types that apply to the branch and gate merges
	// but that the client does not turn into checks (workflows, code_scanning,
	// required_deployments), sorted. Callers should warn: nothing evaluates them.
	Unevaluated []string
}

// CommitStatus is the latest status of one context on a commit (the Statuses
// API, which Jenkins and other external CI report through).
type CommitStatus struct {
	Context   string
	State     string // success|pending|failure|error
	UpdatedAt time.Time
	// Creator is who posted the status: the login (an App shows as
	// "<slug>[bot]") and the account type ("Bot" or "User").
	Creator     string
	CreatorType string
	// AppID is the id of the App that created the status. The client leaves it
	// zero (GitHub does not return it); the merger fills it from AppID lookups.
	AppID int64
}

// CheckRun is the latest run of one check on a commit.
type CheckRun struct {
	ID          int64     // higher is newer; filter=latest does not merge runs across check suites
	CompletedAt time.Time // zero until completed
	Name        string
	AppID       int64
	Status      string // queued|in_progress|completed|waiting|requested|pending
	Conclusion  string // set when Status is completed
}
