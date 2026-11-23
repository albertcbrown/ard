// Package fake is a stateful, concurrency-safe in-memory github.Client for
// unit tests. It mirrors the GitHub behaviours the conformance suite asserts
// (see clienttest) and records every Writer call.
package fake

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/github"
)

// Call is one recorded Writer invocation.
type Call struct {
	Method string
	Args   []any
}

// Option configures New.
type Option func(*state)

// WithIdentity sets the identity returned by Identity and used to attribute
// writes. The default login is "ard-test[bot]".
func WithIdentity(id github.Identity) Option { return func(s *state) { s.identity = id } }

// WithClock replaces time.Now.
func WithClock(now func() time.Time) Option { return func(s *state) { s.now = now } }

type item struct {
	issue      github.Issue
	isPR       bool
	draft      bool
	merged     bool
	headRef    string
	headSHA    string
	baseRef    string
	mergeSt    string
	comments   []github.Comment
	timeline   []github.TimelineEvent
	files      []github.PRFile
	reviews    []github.ReviewComment
	reviewList []github.Review
}

type state struct {
	mu       sync.Mutex
	identity github.Identity
	now      func() time.Time
	next     int
	nextID   int64
	items    map[int]*item
	labels   map[string]github.Label
	perms    map[string]github.Permission
	branches map[string]bool
	pinned   map[int]bool
	writes   []Call
	fail     map[string][]error
	required map[string]github.RequiredChecksResult
	runs     map[string][]github.CheckRun
}

// Fake implements github.Client. Methods on the value returned by As attribute
// writes to a different actor but share all state.
type Fake struct {
	s     *state
	actor github.Identity
}

var _ github.Client = (*Fake)(nil)

// New returns an empty fake repository.
func New(opts ...Option) *Fake {
	s := &state{
		identity: github.Identity{Login: "ard-test[bot]", ID: 1000, Kind: github.AuthApp},
		now:      time.Now,
		next:     1,
		nextID:   1,
		items:    map[int]*item{},
		labels:   map[string]github.Label{},
		perms:    map[string]github.Permission{},
		branches: map[string]bool{},
		pinned:   map[int]bool{},
		fail:     map[string][]error{},
		required: map[string]github.RequiredChecksResult{},
		runs:     map[string][]github.CheckRun{},
	}
	for _, o := range opts {
		o(s)
	}
	return &Fake{s: s, actor: s.identity}
}

// As returns a client sharing this fake's state whose writes are attributed to login.
func (f *Fake) As(login string) github.Client {
	return &Fake{s: f.s, actor: github.Identity{Login: login, Kind: github.AuthToken}}
}

// ---- seeding (bypasses the write log) ----

// SeedIssue adds an issue; Number 0 takes the next number. It returns the number.
func (f *Fake) SeedIssue(is github.Issue) int {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	it := &item{issue: is}
	f.s.seed(it)
	return it.issue.Number
}

// SeedPR adds a pull request (and its issue view); Number 0 takes the next number.
func (f *Fake) SeedPR(pr github.PullRequest) int {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	it := &item{
		isPR: true, draft: pr.Draft, merged: pr.Merged,
		headRef: pr.HeadRef, headSHA: pr.HeadSHA, baseRef: pr.BaseRef, mergeSt: pr.MergeableState,
		issue: github.Issue{
			Number: pr.Number, NodeID: pr.NodeID, Title: pr.Title, Body: pr.Body, State: pr.State,
			Author: pr.Author, Labels: pr.Labels, IsPR: true, URL: pr.URL,
		},
	}
	if it.baseRef == "" {
		it.baseRef = "main"
	}
	if it.mergeSt == "" {
		it.mergeSt = "clean"
	}
	f.s.seed(it)
	if it.headRef != "" {
		f.s.branches[it.headRef] = true
	}
	return it.issue.Number
}

// SeedComment appends a comment to issue or PR n without a timeline event.
func (f *Fake) SeedComment(n int, c github.Comment) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	it := f.s.must(n)
	if c.ID == 0 {
		c.ID = f.s.id()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = f.s.now()
	}
	it.comments = append(it.comments, c)
}

// SeedTimeline appends a raw timeline event.
func (f *Fake) SeedTimeline(n int, ev github.TimelineEvent) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = f.s.now()
	}
	it := f.s.must(n)
	it.timeline = append(it.timeline, ev)
}

// SetPermission sets a user's collaborator permission.
func (f *Fake) SetPermission(login string, p github.Permission) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	f.s.perms[login] = p
}

// SetPRFiles sets the files changed by PR n.
func (f *Fake) SetPRFiles(n int, files []github.PRFile) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	f.s.must(n).files = append([]github.PRFile(nil), files...)
}

// SetRequiredChecks sets the required status checks of branch (rules readable).
func (f *Fake) SetRequiredChecks(branch string, cs ...github.RequiredCheck) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	f.s.required[branch] = github.RequiredChecksResult{Available: true, Checks: append([]github.RequiredCheck(nil), cs...)}
}

// SetRulesUnavailable makes RequiredChecks report no rules API for branch, as
// a free-plan private repository does.
func (f *Fake) SetRulesUnavailable(branch string) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	f.s.required[branch] = github.RequiredChecksResult{}
}

// SetCheckRuns sets the check runs reported on sha.
func (f *Fake) SetCheckRuns(sha string, rs ...github.CheckRun) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	f.s.runs[sha] = append([]github.CheckRun(nil), rs...)
}

// SetMergeableState sets PR n's mergeable_state.
func (f *Fake) SetMergeableState(n int, st string) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	f.s.must(n).mergeSt = st
}

// SetHeadSHA moves PR n's head, as a push would.
func (f *Fake) SetHeadSHA(n int, sha string) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	f.s.must(n).headSHA = sha
}

// ---- inspection and faults ----

// Writes returns every Writer call, in order. Seeds are excluded.
func (f *Fake) Writes() []Call {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	return append([]Call(nil), f.s.writes...)
}

// FailNext makes the next call of method return err, once. Calls queue.
func (f *Fake) FailNext(method string, err error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	f.s.fail[method] = append(f.s.fail[method], err)
}

// Pinned reports whether issue n is pinned.
func (f *Fake) Pinned(n int) bool {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	return f.s.pinned[n]
}

// TransferIssue mirrors GitHub moving issue n to another repository: every
// later call on n returns ErrNotFound and n leaves lists and search. Numbers
// are not reused. It bypasses the write log and panics on a PR or unknown n.
func (f *Fake) TransferIssue(n int) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if it := f.s.must(n); it.isPR {
		panic(fmt.Sprintf("fake: #%d is a pull request and cannot be transferred", n))
	}
	delete(f.s.items, n)
	delete(f.s.pinned, n)
}

// ---- internals ----

func (s *state) id() int64 { s.nextID++; return s.nextID }

func (s *state) seed(it *item) {
	is := &it.issue
	if is.Number == 0 {
		is.Number = s.next
	}
	if is.Number >= s.next {
		s.next = is.Number + 1
	}
	if is.State == "" {
		is.State = github.StateOpen
	}
	now := s.now()
	if is.CreatedAt.IsZero() {
		is.CreatedAt = now
	}
	if is.UpdatedAt.IsZero() {
		is.UpdatedAt = is.CreatedAt
	}
	if is.NodeID == "" {
		is.NodeID = fmt.Sprintf("NODE_%d", is.Number)
	}
	if is.URL == "" {
		is.URL = fmt.Sprintf("https://github.com/fake/repo/issues/%d", is.Number)
	}
	is.Labels = append([]string(nil), is.Labels...)
	for _, l := range is.Labels {
		s.ensureLabel(l)
	}
	if it.isPR && it.headSHA == "" {
		it.headSHA = fmt.Sprintf("sha-%d", is.Number)
	}
	s.items[is.Number] = it
}

func (s *state) ensureLabel(name string) {
	if _, ok := s.labels[name]; !ok {
		s.labels[name] = github.Label{Name: name, Color: "ededed"}
	}
}

func (s *state) must(n int) *item {
	it, ok := s.items[n]
	if !ok {
		panic(fmt.Sprintf("fake: no issue or PR #%d", n))
	}
	return it
}

func (s *state) get(n int) (*item, error) {
	it, ok := s.items[n]
	if !ok {
		return nil, fmt.Errorf("#%d: %w", n, github.ErrNotFound)
	}
	return it, nil
}

func (s *state) event(it *item, actor github.Identity, ev github.TimelineEvent) {
	ev.Actor = actor.Login
	ev.CreatedAt = s.now()
	it.timeline = append(it.timeline, ev)
	it.issue.UpdatedAt = ev.CreatedAt
}

// begin takes the lock, logs a write and pops an injected failure.
func (f *Fake) begin(method string, args ...any) (unlock func(), err error) {
	f.s.mu.Lock()
	f.s.writes = append(f.s.writes, Call{Method: method, Args: args})
	return f.s.mu.Unlock, f.s.popFail(method)
}

func (s *state) popFail(method string) error {
	q := s.fail[method]
	if len(q) == 0 {
		return nil
	}
	s.fail[method] = q[1:]
	return q[0]
}

// beginRead takes the lock and pops an injected failure (reads are not logged).
func (f *Fake) beginRead(method string) (unlock func(), err error) {
	f.s.mu.Lock()
	return f.s.mu.Unlock, f.s.popFail(method)
}

func (it *item) copyIssue() github.Issue {
	is := it.issue
	is.Labels = append([]string(nil), is.Labels...)
	if is.ClosedAt != nil {
		t := *is.ClosedAt
		is.ClosedAt = &t
	}
	return is
}

func (it *item) copyPR() github.PullRequest {
	is := it.issue
	pr := github.PullRequest{
		Number: is.Number, NodeID: is.NodeID, Title: is.Title, Body: is.Body, State: is.State,
		Draft: it.draft, Merged: it.merged, Author: is.Author,
		Labels:  append([]string(nil), is.Labels...),
		HeadRef: it.headRef, HeadSHA: it.headSHA, BaseRef: it.baseRef,
		MergeableState: it.mergeSt, URL: is.URL,
	}
	if is.State == github.StateOpen {
		t := it.mergeSt != "dirty"
		pr.Mergeable = &t
	}
	return pr
}

func hasLabel(labels []string, l string) bool {
	for _, x := range labels {
		if x == l {
			return true
		}
	}
	return false
}

func sortedItems(m map[int]*item) []*item {
	out := make([]*item, 0, len(m))
	for _, it := range m {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].issue.Number < out[j].issue.Number })
	return out
}

// ---- Reader ----

func (f *Fake) Identity(ctx context.Context) (github.Identity, error) {
	unlock, err := f.beginRead("Identity")
	defer unlock()
	if err != nil {
		return github.Identity{}, err
	}
	return f.actor, nil
}

func (f *Fake) GetIssue(ctx context.Context, n int) (github.Issue, error) {
	unlock, err := f.beginRead("GetIssue")
	defer unlock()
	if err != nil {
		return github.Issue{}, err
	}
	it, err := f.s.get(n)
	if err != nil {
		return github.Issue{}, fmt.Errorf("github: get issue %d: %w", n, err)
	}
	return it.copyIssue(), nil
}

// ListIssues returns matches oldest (lowest number) first.
func (f *Fake) ListIssues(ctx context.Context, o github.ListIssuesOpts) ([]github.Issue, error) {
	unlock, err := f.beginRead("ListIssues")
	defer unlock()
	if err != nil {
		return nil, err
	}
	want := o.State
	if want == "" {
		want = github.StateOpen
	}
	var out []github.Issue
	for _, it := range sortedItems(f.s.items) {
		is := it.issue
		if it.isPR && !o.IncludePRs {
			continue
		}
		if want != github.StateAll && is.State != want {
			continue
		}
		if !o.Since.IsZero() && is.UpdatedAt.Before(o.Since) {
			continue
		}
		ok := true
		for _, l := range o.Labels {
			if !hasLabel(is.Labels, l) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		out = append(out, it.copyIssue())
		if o.Max > 0 && len(out) >= o.Max {
			break
		}
	}
	return out, nil
}

func (f *Fake) SearchIssues(ctx context.Context, q string, o github.SearchOpts) ([]github.Issue, error) {
	unlock, err := f.beginRead("SearchIssues")
	defer unlock()
	if err != nil {
		return nil, err
	}
	terms, err := parseSearch(q)
	if err != nil {
		return nil, fmt.Errorf("github: search %q: %w", q, err)
	}
	var matched []*item
	for _, it := range sortedItems(f.s.items) {
		if terms.match(it) {
			matched = append(matched, it)
		}
	}
	switch o.Sort {
	case "created":
		sort.SliceStable(matched, func(i, j int) bool { return matched[i].issue.CreatedAt.Before(matched[j].issue.CreatedAt) })
	case "updated":
		sort.SliceStable(matched, func(i, j int) bool { return matched[i].issue.UpdatedAt.Before(matched[j].issue.UpdatedAt) })
	}
	if o.Order == "desc" {
		for i, j := 0, len(matched)-1; i < j; i, j = i+1, j-1 {
			matched[i], matched[j] = matched[j], matched[i]
		}
	}
	max := o.Max
	if max <= 0 {
		max = 100
	}
	if max > 1000 {
		max = 1000
	}
	if len(matched) > max {
		matched = matched[:max]
	}
	out := make([]github.Issue, len(matched))
	for i, it := range matched {
		out[i] = it.copyIssue()
	}
	return out, nil
}

func (f *Fake) ListComments(ctx context.Context, n int, since time.Time) ([]github.Comment, error) {
	unlock, err := f.beginRead("ListComments")
	defer unlock()
	if err != nil {
		return nil, err
	}
	it, err := f.s.get(n)
	if err != nil {
		return nil, fmt.Errorf("github: list comments %d: %w", n, err)
	}
	var out []github.Comment
	for _, c := range it.comments {
		if !since.IsZero() && c.CreatedAt.Before(since) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (f *Fake) ListTimeline(ctx context.Context, n int) ([]github.TimelineEvent, error) {
	unlock, err := f.beginRead("ListTimeline")
	defer unlock()
	if err != nil {
		return nil, err
	}
	it, err := f.s.get(n)
	if err != nil {
		return nil, fmt.Errorf("github: list timeline %d: %w", n, err)
	}
	return append([]github.TimelineEvent(nil), it.timeline...), nil
}

func (f *Fake) ListLabels(ctx context.Context) ([]github.Label, error) {
	unlock, err := f.beginRead("ListLabels")
	defer unlock()
	if err != nil {
		return nil, err
	}
	out := make([]github.Label, 0, len(f.s.labels))
	for _, l := range f.s.labels {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *Fake) GetPermission(ctx context.Context, login string) (github.Permission, error) {
	unlock, err := f.beginRead("GetPermission")
	defer unlock()
	if err != nil {
		return github.Permission{}, err
	}
	if p, ok := f.s.perms[login]; ok {
		return p, nil
	}
	return github.Permission{Level: "none"}, nil
}

func (f *Fake) GetPR(ctx context.Context, n int) (github.PullRequest, error) {
	unlock, err := f.beginRead("GetPR")
	defer unlock()
	if err != nil {
		return github.PullRequest{}, err
	}
	it, err := f.s.get(n)
	if err != nil || !it.isPR {
		return github.PullRequest{}, fmt.Errorf("github: get PR %d: %w", n, github.ErrNotFound)
	}
	return it.copyPR(), nil
}

func (f *Fake) ListPRs(ctx context.Context, o github.ListPRsOpts) ([]github.PullRequest, error) {
	unlock, err := f.beginRead("ListPRs")
	defer unlock()
	if err != nil {
		return nil, err
	}
	want := o.State
	if want == "" {
		want = github.StateOpen
	}
	var out []github.PullRequest
	for _, it := range sortedItems(f.s.items) {
		if !it.isPR || (want != github.StateAll && it.issue.State != want) {
			continue
		}
		if o.Head != "" && it.headRef != o.Head {
			continue
		}
		if o.Base != "" && it.baseRef != o.Base {
			continue
		}
		out = append(out, it.copyPR())
	}
	return out, nil
}

func (f *Fake) ListPRFiles(ctx context.Context, n int) ([]github.PRFile, error) {
	unlock, err := f.beginRead("ListPRFiles")
	defer unlock()
	if err != nil {
		return nil, err
	}
	it, err := f.s.get(n)
	if err != nil || !it.isPR {
		return nil, fmt.Errorf("github: list PR files %d: %w", n, github.ErrNotFound)
	}
	return append([]github.PRFile(nil), it.files...), nil
}

func (f *Fake) ListReviewComments(ctx context.Context, n int) ([]github.ReviewComment, error) {
	unlock, err := f.beginRead("ListReviewComments")
	defer unlock()
	if err != nil {
		return nil, err
	}
	it, err := f.s.get(n)
	if err != nil || !it.isPR {
		return nil, fmt.Errorf("github: list review comments %d: %w", n, github.ErrNotFound)
	}
	return append([]github.ReviewComment(nil), it.reviews...), nil
}

func (f *Fake) ListReviews(ctx context.Context, n int) ([]github.Review, error) {
	unlock, err := f.beginRead("ListReviews")
	defer unlock()
	if err != nil {
		return nil, err
	}
	it, err := f.s.get(n)
	if err != nil || !it.isPR {
		return nil, fmt.Errorf("github: list reviews %d: %w", n, github.ErrNotFound)
	}
	return append([]github.Review(nil), it.reviewList...), nil
}

// RequiredChecks returns what SetRequiredChecks or SetRulesUnavailable seeded;
// by default the rules are readable and empty.
func (f *Fake) RequiredChecks(ctx context.Context, branch string) (github.RequiredChecksResult, error) {
	unlock, err := f.beginRead("RequiredChecks")
	defer unlock()
	if err != nil {
		return github.RequiredChecksResult{}, err
	}
	r, ok := f.s.required[branch]
	if !ok {
		return github.RequiredChecksResult{Available: true}, nil
	}
	r.Checks = append([]github.RequiredCheck(nil), r.Checks...)
	return r, nil
}

func (f *Fake) ListCheckRuns(ctx context.Context, sha string) ([]github.CheckRun, error) {
	unlock, err := f.beginRead("ListCheckRuns")
	defer unlock()
	if err != nil {
		return nil, err
	}
	return append([]github.CheckRun(nil), f.s.runs[sha]...), nil
}

// ---- Writer ----

func (f *Fake) CreateIssue(ctx context.Context, in github.NewIssue) (github.Issue, error) {
	unlock, err := f.begin("CreateIssue", in)
	defer unlock()
	if err != nil {
		return github.Issue{}, err
	}
	it := &item{issue: github.Issue{Title: in.Title, Body: in.Body, Author: f.actor.Login}}
	f.s.seed(it)
	for _, l := range dedupe(in.Labels) {
		f.s.ensureLabel(l)
		it.issue.Labels = append(it.issue.Labels, l)
		f.s.event(it, f.actor, github.TimelineEvent{Event: "labeled", Label: l})
	}
	return it.copyIssue(), nil
}

func (f *Fake) UpdateIssue(ctx context.Context, n int, u github.IssueUpdate) error {
	unlock, err := f.begin("UpdateIssue", n, u)
	defer unlock()
	if err != nil {
		return err
	}
	it, err := f.s.get(n)
	if err != nil {
		return fmt.Errorf("github: update issue %d: %w", n, err)
	}
	if u.Title != nil {
		it.issue.Title = *u.Title
	}
	if u.Body != nil {
		it.issue.Body = *u.Body
	}
	it.issue.UpdatedAt = f.s.now()
	if u.State != nil {
		switch *u.State {
		case github.StateClosed:
			if it.issue.State != github.StateClosed {
				now := f.s.now()
				it.issue.State = github.StateClosed
				it.issue.ClosedAt = &now
				it.issue.StateReason = github.ReasonCompleted
				if u.StateReason != nil {
					it.issue.StateReason = *u.StateReason
				}
				f.s.event(it, f.actor, github.TimelineEvent{Event: "closed"})
			}
		case github.StateOpen:
			if it.issue.State != github.StateOpen {
				it.issue.State = github.StateOpen
				it.issue.ClosedAt = nil
				it.issue.StateReason = github.ReasonReopened
				f.s.event(it, f.actor, github.TimelineEvent{Event: "reopened"})
			}
		default:
			return fmt.Errorf("github: update issue %d: invalid state %q", n, *u.State)
		}
	}
	return nil
}

func (f *Fake) PinIssue(ctx context.Context, n int) error {
	unlock, err := f.begin("PinIssue", n)
	defer unlock()
	if err != nil {
		return err
	}
	if _, err := f.s.get(n); err != nil {
		return fmt.Errorf("github: pin issue %d: %w", n, err)
	}
	f.s.pinned[n] = true
	return nil
}

func (f *Fake) AddLabels(ctx context.Context, n int, labels ...string) error {
	unlock, err := f.begin("AddLabels", n, labels)
	defer unlock()
	if err != nil {
		return err
	}
	it, err := f.s.get(n)
	if err != nil {
		return fmt.Errorf("github: add labels %d: %w", n, err)
	}
	for _, l := range dedupe(labels) {
		if hasLabel(it.issue.Labels, l) {
			continue
		}
		f.s.ensureLabel(l)
		it.issue.Labels = append(it.issue.Labels, l)
		f.s.event(it, f.actor, github.TimelineEvent{Event: "labeled", Label: l})
	}
	return nil
}

func (f *Fake) RemoveLabel(ctx context.Context, n int, label string) error {
	unlock, err := f.begin("RemoveLabel", n, label)
	defer unlock()
	if err != nil {
		return err
	}
	it, err := f.s.get(n)
	if err != nil {
		return fmt.Errorf("github: remove label %d: %w", n, err)
	}
	for i, l := range it.issue.Labels {
		if l == label {
			it.issue.Labels = append(it.issue.Labels[:i:i], it.issue.Labels[i+1:]...)
			f.s.event(it, f.actor, github.TimelineEvent{Event: "unlabeled", Label: label})
			break
		}
	}
	return nil
}

func (f *Fake) CreateLabel(ctx context.Context, l github.Label) error {
	unlock, err := f.begin("CreateLabel", l)
	defer unlock()
	if err != nil {
		return err
	}
	if _, ok := f.s.labels[l.Name]; ok {
		return fmt.Errorf("github: create label %q: %w", l.Name, github.ErrConflict)
	}
	if l.Color == "" {
		l.Color = "ededed"
	}
	f.s.labels[l.Name] = l
	return nil
}

func (f *Fake) UpdateLabel(ctx context.Context, name string, l github.Label) error {
	unlock, err := f.begin("UpdateLabel", name, l)
	defer unlock()
	if err != nil {
		return err
	}
	old, ok := f.s.labels[name]
	if !ok {
		return fmt.Errorf("github: update label %q: %w", name, github.ErrNotFound)
	}
	if l.Name == "" {
		l.Name = name
	}
	if l.Color == "" {
		l.Color = old.Color
	}
	delete(f.s.labels, name)
	f.s.labels[l.Name] = l
	if l.Name != name {
		for _, it := range f.s.items {
			for i, x := range it.issue.Labels {
				if x == name {
					it.issue.Labels[i] = l.Name
				}
			}
		}
	}
	return nil
}

func (f *Fake) CreateComment(ctx context.Context, n int, body string) (github.Comment, error) {
	unlock, err := f.begin("CreateComment", n, body)
	defer unlock()
	if err != nil {
		return github.Comment{}, err
	}
	it, err := f.s.get(n)
	if err != nil {
		return github.Comment{}, fmt.Errorf("github: create comment %d: %w", n, err)
	}
	c := github.Comment{ID: f.s.id(), Author: f.actor.Login, Body: body, CreatedAt: f.s.now()}
	c.URL = fmt.Sprintf("%s#issuecomment-%d", it.issue.URL, c.ID)
	it.comments = append(it.comments, c)
	f.s.event(it, f.actor, github.TimelineEvent{Event: "commented", Body: body})
	return c, nil
}

func (f *Fake) CreatePR(ctx context.Context, in github.NewPR) (github.PullRequest, error) {
	unlock, err := f.begin("CreatePR", in)
	defer unlock()
	if err != nil {
		return github.PullRequest{}, err
	}
	if in.Head == "" || in.Base == "" {
		return github.PullRequest{}, fmt.Errorf("github: create PR: head and base are required")
	}
	it := &item{
		isPR: true, draft: in.Draft, headRef: in.Head, baseRef: in.Base, mergeSt: "clean",
		issue: github.Issue{Title: in.Title, Body: in.Body, Author: f.actor.Login, IsPR: true},
	}
	if in.Draft {
		it.mergeSt = "draft"
	}
	f.s.seed(it)
	f.s.branches[in.Head] = true
	return it.copyPR(), nil
}

func (f *Fake) UpdatePR(ctx context.Context, n int, u github.PRUpdate) error {
	unlock, err := f.begin("UpdatePR", n, u)
	defer unlock()
	if err != nil {
		return err
	}
	it, err := f.s.get(n)
	if err != nil || !it.isPR {
		return fmt.Errorf("github: update PR %d: %w", n, github.ErrNotFound)
	}
	if u.Title != nil {
		it.issue.Title = *u.Title
	}
	if u.Body != nil {
		it.issue.Body = *u.Body
	}
	it.issue.UpdatedAt = f.s.now()
	return nil
}

func (f *Fake) MarkReady(ctx context.Context, n int) error {
	unlock, err := f.begin("MarkReady", n)
	defer unlock()
	if err != nil {
		return err
	}
	it, err := f.s.get(n)
	if err != nil || !it.isPR {
		return fmt.Errorf("github: mark ready %d: %w", n, github.ErrNotFound)
	}
	it.draft = false
	if it.mergeSt == "draft" {
		it.mergeSt = "clean"
	}
	it.issue.UpdatedAt = f.s.now()
	return nil
}

func (f *Fake) CreateReview(ctx context.Context, n int, in github.ReviewInput) (int64, error) {
	unlock, err := f.begin("CreateReview", n, in)
	defer unlock()
	if err != nil {
		return 0, err
	}
	it, err := f.s.get(n)
	if err != nil || !it.isPR {
		return 0, fmt.Errorf("github: create review %d: %w", n, github.ErrNotFound)
	}
	rid := f.s.id()
	it.reviewList = append(it.reviewList, github.Review{
		ID: rid, Author: f.actor.Login, Body: in.Body, State: "COMMENTED",
		CommitSHA: in.CommitSHA, SubmittedAt: f.s.now(),
	})
	for _, c := range in.Comments {
		it.reviews = append(it.reviews, github.ReviewComment{
			ID: f.s.id(), Author: f.actor.Login, Path: c.Path, Line: c.Line, Body: c.Body,
			CommitSHA: in.CommitSHA, CreatedAt: f.s.now(),
		})
	}
	it.issue.UpdatedAt = f.s.now()
	return rid, nil
}

func (f *Fake) UpdateBranch(ctx context.Context, n int, expectedHeadSHA string) error {
	unlock, err := f.begin("UpdateBranch", n, expectedHeadSHA)
	defer unlock()
	if err != nil {
		return err
	}
	it, err := f.s.get(n)
	if err != nil || !it.isPR {
		return fmt.Errorf("github: update branch %d: %w", n, github.ErrNotFound)
	}
	if expectedHeadSHA != it.headSHA {
		return fmt.Errorf("github: update branch %d: head moved: %w", n, github.ErrConflict)
	}
	if it.mergeSt == "dirty" {
		return fmt.Errorf("github: update branch %d: merge conflict: %w", n, github.ErrConflict)
	}
	it.headSHA += "+base"
	it.mergeSt = "clean"
	return nil
}

func (f *Fake) MergePR(ctx context.Context, n int, in github.MergeInput) (github.MergeResult, error) {
	unlock, err := f.begin("MergePR", n, in)
	defer unlock()
	if err != nil {
		return github.MergeResult{}, err
	}
	it, err := f.s.get(n)
	if err != nil || !it.isPR {
		return github.MergeResult{}, fmt.Errorf("github: merge PR %d: %w", n, github.ErrNotFound)
	}
	if in.SHA == "" {
		return github.MergeResult{}, fmt.Errorf("github: merge PR %d: head SHA is required", n)
	}
	if in.SHA != it.headSHA {
		return github.MergeResult{}, fmt.Errorf("github: merge PR %d: head moved: %w", n, github.ErrConflict)
	}
	if it.issue.State != github.StateOpen || it.draft || it.mergeSt == "dirty" || it.mergeSt == "blocked" {
		return github.MergeResult{}, fmt.Errorf("github: merge PR %d: %w", n, github.ErrNotMergeable)
	}
	now := f.s.now()
	it.merged = true
	it.issue.State = github.StateClosed
	it.issue.ClosedAt = &now
	it.issue.StateReason = github.ReasonCompleted
	f.s.event(it, f.actor, github.TimelineEvent{Event: "merged"})
	f.s.event(it, f.actor, github.TimelineEvent{Event: "closed"})
	if it.baseRef == "main" {
		f.closeLinked(it.issue.Body)
	}
	return github.MergeResult{SHA: fmt.Sprintf("merge-%d", n)}, nil
}

var closesRe = regexp.MustCompile(`(?m)^Closes #(\d+)\s*$`)

// closeLinked closes every open issue named by a "Closes #<n>" line, as GitHub
// does for a PR merged into the default branch. Caller holds the lock.
func (f *Fake) closeLinked(body string) {
	for _, m := range closesRe.FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(m[1])
		it, err := f.s.get(n)
		if err != nil || it.isPR || it.issue.State != github.StateOpen {
			continue
		}
		now := f.s.now()
		it.issue.State = github.StateClosed
		it.issue.ClosedAt = &now
		it.issue.StateReason = github.ReasonCompleted
		f.s.event(it, f.actor, github.TimelineEvent{Event: "closed"})
	}
}

func (f *Fake) DeleteBranch(ctx context.Context, branch string) error {
	unlock, err := f.begin("DeleteBranch", branch)
	defer unlock()
	if err != nil {
		return err
	}
	delete(f.s.branches, branch)
	return nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ---- search evaluator ----

type term struct {
	neg        bool
	key, value string
}

type searchTerms []term

func parseSearch(q string) (searchTerms, error) {
	var toks []string
	var cur strings.Builder
	inQuote, has := false, false
	for _, r := range q {
		switch {
		case r == '"':
			inQuote = !inQuote
			has = true
			cur.WriteRune(r)
		case (r == ' ' || r == '\t') && !inQuote:
			if has {
				toks = append(toks, cur.String())
				cur.Reset()
				has = false
			}
		default:
			has = true
			cur.WriteRune(r)
		}
	}
	if inQuote {
		return nil, fmt.Errorf("unterminated quote")
	}
	if has {
		toks = append(toks, cur.String())
	}
	var out searchTerms
	for _, tok := range toks {
		t := term{}
		if strings.HasPrefix(tok, "-") {
			t.neg = true
			tok = tok[1:]
		}
		if len(tok) >= 2 && strings.HasPrefix(tok, `"`) && strings.HasSuffix(tok, `"`) {
			// Quoted free text: matched against the title (the only field
			// the fake supports, so in:title is required alongside it).
			t.key, t.value = "text", strings.Trim(tok, `"`)
			out = append(out, t)
			continue
		}
		k, v, ok := strings.Cut(tok, ":")
		if !ok {
			return nil, fmt.Errorf("unsupported free-text term %q", tok)
		}
		v = strings.Trim(v, `"`)
		switch k {
		case "repo":
		case "is":
			if v != "open" && v != "closed" && v != "issue" && v != "pr" {
				return nil, fmt.Errorf("unsupported qualifier is:%s", v)
			}
		case "in":
			if v != "title" {
				return nil, fmt.Errorf("unsupported qualifier in:%s", v)
			}
		case "label":
		default:
			return nil, fmt.Errorf("unsupported qualifier %q", k)
		}
		t.key, t.value = k, v
		out = append(out, t)
	}
	var hasText, hasInTitle bool
	for _, t := range out {
		hasText = hasText || t.key == "text"
		hasInTitle = hasInTitle || (t.key == "in" && !t.neg)
	}
	if hasText && !hasInTitle {
		return nil, errors.New("fake: quoted text requires in:title")
	}
	return out, nil
}

func (ts searchTerms) match(it *item) bool {
	for _, t := range ts {
		var ok bool
		switch t.key {
		case "repo", "in":
			continue
		case "text":
			ok = strings.Contains(strings.ToLower(it.issue.Title), strings.ToLower(t.value))
		case "is":
			switch t.value {
			case "open":
				ok = it.issue.State == github.StateOpen
			case "closed":
				ok = it.issue.State == github.StateClosed
			case "issue":
				ok = !it.isPR
			case "pr":
				ok = it.isPR
			}
		case "label":
			ok = hasLabel(it.issue.Labels, t.value)
		}
		if ok == t.neg {
			return false
		}
	}
	return true
}
