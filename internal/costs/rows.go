package costs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/costblock"
)

// rows builds the issue's table rows from state: one per closed job, oldest
// first. A job whose close-out record cannot be read still gets a row (with no
// outcome), so its usage is not lost.
func (l *Loop) rows(ctx context.Context, n int, log *slog.Logger) ([]costblock.Row, error) {
	jobs, err := l.d.Store.ClosedJobsOf(ctx, n)
	if err != nil {
		return nil, err
	}
	usage, err := l.d.Store.IssueUsage(ctx, n)
	if err != nil {
		return nil, err
	}
	runs := map[string]int{}
	var out []costblock.Row
	for _, j := range jobs {
		key := j.Stage + ":" + j.Role
		runs[key]++
		r := costblock.Row{Stage: j.Stage, Role: j.Role, Run: runs[key]}
		if !costblock.ValidRow(r) {
			log.Warn("costs.row_dropped", "job", j.ID, "stage", j.Stage)
			continue
		}
		if u, ok := usage[j.ID]; ok {
			r.Models, r.CostUSD = u.Models, u.CostUSD
			if t := u.Tokens; t != nil {
				r.Tokens = &costblock.Tokens{Input: t.Input, Output: t.Output, CacheWrite: t.CacheWrite, CacheRead: t.CacheRead}
			}
		}
		f, err := closeout.FactsOf(j)
		if err != nil {
			log.Warn("costs.facts_unreadable", "job", j.ID, "error", err)
		} else {
			r.Outcome = outcomeText(f)
			r.PR = f.PR
			r.Link = l.link(j.Stage, n, f)
		}
		out = append(out, r)
	}
	return out, nil
}

// outcomeText maps a close-out outcome to the table's fixed vocabulary. It
// never uses Plan.Reason, which can quote agent text.
func outcomeText(f closeout.CostFacts) string {
	switch f.Outcome {
	case closeout.OutcomeDone, closeout.OutcomeChanges, closeout.OutcomeStale, closeout.OutcomeNeedsHuman, closeout.OutcomeRetry:
		return string(f.Outcome)
	case closeout.OutcomeLimited:
		return "usage limit"
	case closeout.OutcomeFailed:
		switch {
		case f.Cause != "":
			return "failed: " + string(f.Cause)
		case f.ExitCode >= 1 && f.ExitCode <= 999:
			return fmt.Sprintf("failed: exit %d", f.ExitCode)
		}
		return "failed: result"
	}
	return ""
}

// link is where the job's close-out landed, or "" when nothing reached GitHub.
func (l *Loop) link(stage string, n int, f closeout.CostFacts) string {
	if f.Synthetic || n < 0 || l.d.Effects.DryRun() {
		return ""
	}
	base := "https://github.com/" + l.d.Repo.String()
	if stage != closeout.StageReview {
		if f.CommentID > 0 {
			return fmt.Sprintf("%s/issues/%d#issuecomment-%d", base, n, f.CommentID)
		}
		return fmt.Sprintf("%s/issues/%d", base, n)
	}
	if f.PR <= 0 {
		return ""
	}
	pr := fmt.Sprintf("%s/pull/%d", base, f.PR)
	switch {
	case f.CommentID > 0 && (f.ReviewMode == closeout.ReviewInline || f.ReviewMode == closeout.ReviewBody):
		return fmt.Sprintf("%s#pullrequestreview-%d", pr, f.CommentID)
	case f.CommentID > 0 && f.ReviewMode == closeout.ReviewComment:
		return fmt.Sprintf("%s#issuecomment-%d", pr, f.CommentID)
	}
	return pr // ReviewFound: the id may be a review or a comment
}
