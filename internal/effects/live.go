package effects

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
)

// Live performs effects for real.
type Live struct {
	w   github.Writer
	g   *gitx.Git
	log *slog.Logger
}

var _ Effects = (*Live)(nil)

// NewLive wraps a writer and git runner. A nil log uses slog.Default().
func NewLive(w github.Writer, g *gitx.Git, log *slog.Logger) *Live {
	if log == nil {
		log = slog.Default()
	}
	return &Live{w: w, g: g, log: log}
}

func (l *Live) DryRun() bool { return false }

// done logs one effect record and wraps err with the op and target.
func (l *Live) done(ctx context.Context, op string, n int, err error, attrs ...any) error {
	a := append([]any{"op", op, "dry_run", false}, attrs...)
	if n != 0 {
		a = append(a, "target", n)
	}
	if err != nil {
		l.log.WarnContext(ctx, "effect", append(a, "error", err.Error())...)
		if n != 0 {
			return fmt.Errorf("effects: %s #%d: %w", op, n, err)
		}
		return fmt.Errorf("effects: %s: %w", op, err)
	}
	l.log.InfoContext(ctx, "effect", a...)
	return nil
}

// bad rejects synthetic or zero targets before anything is sent.
func bad(op string, n int) error {
	if n <= 0 {
		return fmt.Errorf("effects: %s #%d: %w", op, n, ErrSyntheticTarget)
	}
	return nil
}

func (l *Live) CreateIssue(ctx context.Context, in github.NewIssue) (github.Issue, error) {
	is, err := l.w.CreateIssue(ctx, in)
	return is, l.done(ctx, "CreateIssue", is.Number, err, "title_len", len(in.Title), "body_len", len(in.Body), "labels", in.Labels)
}

func (l *Live) UpdateIssue(ctx context.Context, n int, u github.IssueUpdate) error {
	if err := bad("UpdateIssue", n); err != nil {
		return err
	}
	return l.done(ctx, "UpdateIssue", n, l.w.UpdateIssue(ctx, n, u),
		"body_set", u.Body != nil, "state", ptrStr(u.State), "reason", ptrStr(u.StateReason))
}

func (l *Live) UpdateIssueBody(ctx context.Context, n int, body string) error {
	return l.UpdateIssue(ctx, n, github.IssueUpdate{Body: &body})
}

func (l *Live) PinIssue(ctx context.Context, n int) error {
	if err := bad("PinIssue", n); err != nil {
		return err
	}
	return l.done(ctx, "PinIssue", n, l.w.PinIssue(ctx, n))
}

func (l *Live) AddLabels(ctx context.Context, n int, labels ...string) error {
	if err := bad("AddLabels", n); err != nil {
		return err
	}
	return l.done(ctx, "AddLabels", n, l.w.AddLabels(ctx, n, labels...), "labels", labels)
}

func (l *Live) RemoveLabel(ctx context.Context, n int, label string) error {
	if err := bad("RemoveLabel", n); err != nil {
		return err
	}
	return l.done(ctx, "RemoveLabel", n, l.w.RemoveLabel(ctx, n, label), "label", label)
}

func (l *Live) CreateLabel(ctx context.Context, lb github.Label) error {
	return l.done(ctx, "CreateLabel", 0, l.w.CreateLabel(ctx, lb), "label", lb.Name)
}

func (l *Live) UpdateLabel(ctx context.Context, name string, lb github.Label) error {
	return l.done(ctx, "UpdateLabel", 0, l.w.UpdateLabel(ctx, name, lb), "label", name)
}

func (l *Live) CreateComment(ctx context.Context, n int, body string) (github.Comment, error) {
	if err := bad("CreateComment", n); err != nil {
		return github.Comment{}, err
	}
	c, err := l.w.CreateComment(ctx, n, body)
	return c, l.done(ctx, "CreateComment", n, err, "body_len", len(body))
}

func (l *Live) CreatePR(ctx context.Context, in github.NewPR) (github.PullRequest, error) {
	pr, err := l.w.CreatePR(ctx, in)
	return pr, l.done(ctx, "CreatePR", pr.Number, err, "head", in.Head, "base", in.Base, "draft", in.Draft, "body_len", len(in.Body))
}

func (l *Live) UpdatePR(ctx context.Context, n int, u github.PRUpdate) error {
	if err := bad("UpdatePR", n); err != nil {
		return err
	}
	return l.done(ctx, "UpdatePR", n, l.w.UpdatePR(ctx, n, u), "title_set", u.Title != nil, "body_set", u.Body != nil)
}

func (l *Live) MarkReady(ctx context.Context, n int) error {
	if err := bad("MarkReady", n); err != nil {
		return err
	}
	return l.done(ctx, "MarkReady", n, l.w.MarkReady(ctx, n))
}

func (l *Live) CreateReview(ctx context.Context, n int, in github.ReviewInput) (int64, error) {
	if err := bad("CreateReview", n); err != nil {
		return 0, err
	}
	id, err := l.w.CreateReview(ctx, n, in)
	return id, l.done(ctx, "CreateReview", n, err,
		"commit", in.CommitSHA, "body_len", len(in.Body), "inline", len(in.Comments))
}

func (l *Live) UpdateBranch(ctx context.Context, n int, expectedHeadSHA string) error {
	if err := bad("UpdateBranch", n); err != nil {
		return err
	}
	return l.done(ctx, "UpdateBranch", n, l.w.UpdateBranch(ctx, n, expectedHeadSHA), "expected_head", expectedHeadSHA)
}

func (l *Live) MergePR(ctx context.Context, n int, in github.MergeInput) (github.MergeResult, error) {
	if err := bad("MergePR", n); err != nil {
		return github.MergeResult{}, err
	}
	r, err := l.w.MergePR(ctx, n, in)
	return r, l.done(ctx, "MergePR", n, err, "sha", in.SHA, "merge_sha", r.SHA)
}

func (l *Live) DeleteBranch(ctx context.Context, branch string) error {
	return l.done(ctx, "DeleteBranch", 0, l.w.DeleteBranch(ctx, branch), "branch", branch)
}

func (l *Live) Commit(ctx context.Context, op CommitOp) (CommitResult, error) {
	sha, err := l.g.Commit(ctx, op)
	if err != nil {
		// An unchanged tree is a normal outcome, not a warning.
		if isNothing(err) {
			l.log.InfoContext(ctx, "effect", "op", "Commit", "dry_run", false, "dir", op.Dir, "result", "nothing to commit")
			return CommitResult{}, err
		}
		return CommitResult{}, l.done(ctx, "Commit", 0, err, "dir", op.Dir)
	}
	return CommitResult{SHA: sha}, l.done(ctx, "Commit", 0, nil, "dir", op.Dir, "sha", sha, "message_len", len(op.Message))
}

func (l *Live) Push(ctx context.Context, op PushOp) error {
	return l.done(ctx, "Push", 0, l.g.Push(ctx, op), "remote", op.Remote, "ref", op.Ref, "force_with_lease", op.ForceWithLease)
}

func ptrStr[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}
