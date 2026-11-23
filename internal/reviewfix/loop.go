package reviewfix

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/prgate"
	"github.com/albertcbrown/ard/internal/state"
)

const defaultInterval = 5 * time.Minute

// Reader is the part of github.Reader the loop uses.
type Reader interface {
	Identity(ctx context.Context) (github.Identity, error)
	GetIssue(ctx context.Context, n int) (github.Issue, error)
	GetPR(ctx context.Context, n int) (github.PullRequest, error)
	SearchIssues(ctx context.Context, q string, o github.SearchOpts) ([]github.Issue, error)
	ListComments(ctx context.Context, n int, since time.Time) ([]github.Comment, error)
}

// Effects is the part of effects.Effects the loop uses.
type Effects interface {
	AddLabels(ctx context.Context, n int, labels ...string) error
	RemoveLabel(ctx context.Context, n int, label string) error
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
	DryRun() bool
}

// Store is the part of *state.Store the loop uses.
type Store interface {
	EventStore
	RunningJobs(ctx context.Context) ([]state.Job, error)
	CloseoutsOf(ctx context.Context, issue int, stage string) ([]state.Job, error)
}

// Git is the read side of *gitx.Git, run against the controller's clone only.
type Git interface {
	FetchRef(ctx context.Context, repoDir, url, refspec string, token gitx.TokenFunc) error
	RevParse(ctx context.Context, dir, ref string) (string, error)
	CodeChanged(ctx context.Context, repoDir, from, to string, exclude ...string) ([]string, error)
}

// Deps are the loop's collaborators.
type Deps struct {
	Reader  Reader
	Effects Effects
	Store   Store
	Git     Git

	RepoDir   string
	RemoteURL string
	Token     gitx.TokenFunc
	Log       *slog.Logger
}

// Loop is the review fix loop. It implements controller.Loop.
type Loop struct{ d Deps }

var _ controller.Loop = (*Loop)(nil)

// New returns the loop.
func New(d Deps) *Loop {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Loop{d: d}
}

// Name implements controller.Loop.
func (*Loop) Name() string { return "reviewfix" }

// Interval implements controller.Loop.
func (*Loop) Interval(c *config.Resolved) time.Duration {
	if c == nil || c.Intervals.Reviewers <= 0 {
		return defaultInterval
	}
	return time.Duration(c.Intervals.Reviewers)
}

// TickTimeout implements controller.TickTimeouter.
func (*Loop) TickTimeout(*config.Resolved) time.Duration { return 5 * time.Minute }

// Claims implements controller.Loop: the loop is skipped while paused.
func (*Loop) Claims() bool { return true }

type tick struct {
	l    *Loop
	cfg  *config.Resolved
	log  *slog.Logger
	self string
	dry  bool
}

// Tick implements controller.Loop.
func (l *Loop) Tick(ctx context.Context, env controller.Env) error {
	d := l.d
	if env.Cfg == nil || len(env.Cfg.RolesOf(config.KindReviewer)) == 0 {
		return nil
	}
	log := env.Log
	if log == nil {
		log = d.Log
	}
	id, err := d.Reader.Identity(ctx)
	if err != nil {
		return fmt.Errorf("reviewfix: identify github user: %w", err)
	}
	t := &tick{l: l, cfg: env.Cfg, log: log, self: id.Login, dry: d.Effects.DryRun()}
	prs, err := t.candidates(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, n := range prs {
		if ctx.Err() != nil {
			break
		}
		if err := t.handle(ctx, n); err != nil {
			log.Error("reviewfix.pr_failed", "pr", n, "error", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// candidates lists the PRs with a :changes label and those of issues whose
// newest round is requested, each once.
func (t *tick) candidates(ctx context.Context) ([]int, error) {
	d := t.l.d
	var out []int
	seen := map[int]bool{}
	add := func(n int) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, role := range t.cfg.RolesOf(config.KindReviewer) {
		q := `is:pr is:open label:"` + labels.Changes(role.Name) + `"`
		hits, err := d.Reader.SearchIssues(ctx, q, github.SearchOpts{Sort: "created", Order: "asc", Max: 50})
		if err != nil {
			t.log.Warn("reviewfix.search_failed", "role", role.Name, "error", err)
			continue
		}
		for _, h := range hits {
			add(h.Number)
		}
	}
	evs, err := d.Store.Events(ctx, state.EventFilter{Kind: state.EventReviewRound})
	if err != nil {
		return nil, fmt.Errorf("reviewfix: read rounds: %w", err)
	}
	rs, err := decodeRounds(evs)
	if err != nil {
		return nil, err
	}
	newest := map[int]Round{}
	var order []int
	for i, r := range rs {
		is := evs[i].Issue
		if _, ok := newest[is]; !ok {
			order = append(order, is)
		}
		newest[is] = r
	}
	for _, is := range order {
		if r := newest[is]; r.Phase == PhaseRequested && r.PR > 0 {
			add(r.PR)
		}
	}
	return out, nil
}

var issueBlockers = []string{labels.NeedsHuman, labels.Hold, labels.ExecutorFailed, labels.TestsFailed}

func (t *tick) handle(ctx context.Context, prN int) error {
	d := t.l.d
	pr, err := d.Reader.GetPR(ctx, prN)
	if err != nil {
		return fmt.Errorf("pr %d: %w", prN, err)
	}
	if pr.State != github.StateOpen || pr.Merged || !strings.EqualFold(pr.Author, t.self) || pr.BaseRef != "main" || prgate.Blocked(pr.Labels) {
		return nil
	}
	worker, n, ok := prgate.ParseWorkerBranch(pr.HeadRef)
	if !ok {
		return nil
	}
	is, err := d.Reader.GetIssue(ctx, n)
	if err != nil {
		return fmt.Errorf("issue %d: %w", n, err)
	}
	if is.State != github.StateOpen || is.IsPR || prgate.HasAny(is.Labels, issueBlockers) {
		return nil
	}
	wr, ok := t.cfg.Role(config.KindWorker, worker)
	if !ok {
		return nil
	}

	// Quiet: every requested reviewer has finished and nothing runs.
	var requested []string
	for _, r := range t.cfg.RolesOf(config.KindReviewer) {
		if !prgate.HasLabel(pr.Labels, labels.Request(labels.StageReview, r.Name)) {
			continue
		}
		if prgate.HasLabel(pr.Labels, labels.Running(labels.StageReview, r.Name)) {
			return nil
		}
		if !prgate.HasLabel(pr.Labels, labels.Done(labels.StageReview, r.Name)) && !prgate.HasLabel(pr.Labels, labels.Changes(r.Name)) {
			return nil
		}
		requested = append(requested, r.Name)
	}
	if len(requested) == 0 {
		return nil
	}
	jobs, err := d.Store.RunningJobs(ctx)
	if err != nil {
		return fmt.Errorf("issue %d: running jobs: %w", n, err)
	}
	for _, j := range jobs {
		if j.Issue == n && (j.Stage == string(labels.StageReview) || j.Stage == string(labels.StageWorker)) {
			return nil
		}
	}

	rs, err := Rounds(ctx, d.Store, n)
	if err != nil {
		return err
	}
	var newest *Round
	if len(rs) > 0 {
		newest = &rs[len(rs)-1]
	}
	if newest != nil && newest.Phase == PhaseRequested {
		return t.landing(ctx, pr, is, worker, n, requested, rs)
	}

	var changed []string
	for _, r := range requested {
		if !prgate.HasLabel(pr.Labels, labels.Changes(r)) {
			continue
		}
		_, ok, err := closeout.ReviewChanged(ctx, d.Store, n, pr.Number, r)
		if err != nil {
			return err
		}
		if !ok {
			t.log.Warn("reviewfix.changes_unproven", "issue", n, "pr", pr.Number, "role", r)
			continue
		}
		changed = append(changed, r)
	}
	if len(changed) == 0 {
		return nil
	}
	if newest != nil && newest.Phase == PhaseCapped && newest.Head == pr.HeadSHA {
		return nil // a human owns it
	}
	if t.dry {
		t.log.Info("reviewfix.would_request", "issue", n, "pr", pr.Number, "roles", changed)
		return nil
	}
	started, err := t.l.RequestRound(ctx, Request{Issue: n, PR: pr.Number, Head: pr.HeadSHA, Worker: worker, Reason: ReasonReview, Roles: changed, Max: wr.MaxReviewRounds})
	if err != nil {
		return err
	}
	if started {
		t.log.Info("reviewfix.request", "issue", n, "pr", pr.Number, "round", Requested(rs)+1, "roles", changed)
	} else {
		t.log.Warn("reviewfix.cap_reached", "issue", n, "pr", pr.Number, "max", wr.MaxReviewRounds)
	}
	return nil
}

// landing handles an issue whose newest round is requested.
func (t *tick) landing(ctx context.Context, pr github.PullRequest, is github.Issue, worker string, n int, requested []string, rs []Round) error {
	d := t.l.d
	R := rs[len(rs)-1]
	doneLabel := labels.Done(labels.StageWorker, worker)
	hasDone := prgate.HasLabel(is.Labels, doneLabel)
	running := prgate.HasLabel(is.Labels, labels.Running(labels.StageWorker, worker))
	claims, err := d.Store.Events(ctx, state.EventFilter{Kind: state.EventClaim, Stage: string(labels.StageWorker), Issue: n})
	if err != nil {
		return fmt.Errorf("issue %d: worker claims: %w", n, err)
	}
	claimed := false
	for _, c := range claims {
		if c.ID > R.ID {
			claimed = true
		}
	}
	switch {
	case !claimed && hasDone:
		if t.dry {
			t.log.Info("reviewfix.would_request", "issue", n, "replay", true)
			return nil
		}
		if err := d.Effects.RemoveLabel(ctx, n, doneLabel); err != nil {
			return fmt.Errorf("issue %d: replay remove worker done label: %w", n, err)
		}
		return nil
	case !claimed || running || !hasDone:
		return nil
	}
	ref := fmt.Sprintf("refs/ard/reviewfix/%d", pr.Number)
	if err := d.Git.FetchRef(ctx, d.RepoDir, d.RemoteURL, "+refs/heads/"+pr.HeadRef+":"+ref, d.Token); err != nil {
		return fmt.Errorf("pr %d: fetch branch: %w", pr.Number, err)
	}
	tip, err := d.Git.RevParse(ctx, d.RepoDir, ref)
	if err != nil {
		return fmt.Errorf("pr %d: %w", pr.Number, err)
	}
	if tip != pr.HeadSHA {
		return nil
	}
	covered, _, err := prgate.Covers(ctx, d.Git, d.RepoDir, R.Head, pr.HeadSHA, "", n)
	if err != nil {
		return fmt.Errorf("pr %d: compare heads: %w", pr.Number, err)
	}
	if covered {
		if t.dry {
			t.log.Info("reviewfix.would_cap", "issue", n, "pr", pr.Number, "why", "no code")
			return nil
		}
		var asking []string
		for _, r := range requested {
			if prgate.HasLabel(pr.Labels, labels.Changes(r)) {
				asking = append(asking, r)
			}
		}
		return t.l.capPath(ctx, n, pr.Number, pr.HeadSHA, worker, Requested(rs), asking, "the last fix round changed no code")
	}
	if t.dry {
		t.log.Info("reviewfix.would_apply", "issue", n, "pr", pr.Number)
		return nil
	}
	for _, r := range requested {
		for _, name := range []string{labels.Changes(r), labels.Done(labels.StageReview, r)} {
			if !prgate.HasLabel(pr.Labels, name) {
				continue
			}
			if err := d.Effects.RemoveLabel(ctx, pr.Number, name); err != nil {
				return fmt.Errorf("pr %d: remove %s: %w", pr.Number, name, err)
			}
		}
	}
	t.log.Info("reviewfix.apply", "issue", n, "pr", pr.Number, "round", R.N)
	return t.l.appendRound(ctx, n, Round{N: R.N, Phase: PhaseApplied, Reason: R.Reason, PR: pr.Number, Head: pr.HeadSHA, Worker: worker, Roles: requested})
}

// capPath hands the issue to a human: one comment (never finding text), the
// needs-human label, then the capped event. A crash replays the first two.
func (l *Loop) capPath(ctx context.Context, issue, pr int, head, worker string, count int, roles []string, why string) error {
	d := l.d
	marker := "<!-- ard:review-rounds head=" + head + " -->"
	text := fmt.Sprintf("Review fix rounds stopped on pull request #%d: %s. Reviewers still asking for changes: %s. A person needs to look at it: raise max_review_rounds, or fix the PR and remove the ard:review:<role>:changes labels so reviewers rerun.",
		pr, why, strings.Join(roles, ", "))
	if len(roles) == 0 {
		text = fmt.Sprintf("Review fix rounds stopped on pull request #%d: %s. A person needs to look at it: raise max_review_rounds, or fix the PR and remove the ard:review:<role>:changes labels so reviewers rerun.", pr, why)
	}
	if err := agentjob.PostOnce(ctx, d.Reader, d.Effects, issue, marker, text); err != nil {
		return err
	}
	if err := d.Effects.AddLabels(ctx, issue, labels.NeedsHuman); err != nil {
		return fmt.Errorf("issue %d: add needs-human: %w", issue, err)
	}
	return l.appendRound(ctx, issue, Round{N: count, Phase: PhaseCapped, Reason: ReasonReview, PR: pr, Head: head, Worker: worker, Roles: roles})
}
