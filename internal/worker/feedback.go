package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/reviewfix"
)

const feedbackCap = 64 << 10

// reviewFeedback builds review-feedback.md for a review fix round: the round,
// the committed reviewer role files and the pull request comments of people.
// It applies only when the issue's newest round is requested.
func (l *Loop) reviewFeedback(ctx context.Context, role config.Role, n int, branch string) (executor.File, bool, error) {
	d := l.d
	rs, err := reviewfix.Rounds(ctx, d.Store, n)
	if err != nil {
		return executor.File{}, false, err
	}
	if len(rs) == 0 || rs[len(rs)-1].Phase != reviewfix.PhaseRequested {
		return executor.File{}, false, nil
	}
	r := rs[len(rs)-1]
	var b strings.Builder
	b.WriteString(agentjob.UntrustedHeader)
	fmt.Fprintf(&b, "Review round %d of %d; reviewers asking for changes: %s.\n", r.N, role.MaxReviewRounds, strings.Join(r.Roles, ", "))
	for _, rr := range r.Roles {
		data, ok, err := d.Git.FileAt(ctx, d.RepoDir, localRef(branch), fmt.Sprintf(".ard/%d/review-%s.md", n, rr), feedbackCap)
		if err != nil {
			return executor.File{}, false, fmt.Errorf("read review-%s.md: %w", rr, err)
		}
		if ok {
			fmt.Fprintf(&b, "\n### review-%s.md\n\n%s\n", rr, data)
		}
	}
	id, err := d.Reader.Identity(ctx)
	if err != nil {
		return executor.File{}, false, err
	}
	prs, err := d.Reader.ListPRs(ctx, github.ListPRsOpts{State: github.StateOpen, Head: branch})
	if err != nil {
		return executor.File{}, false, err
	}
	b.WriteString("\n### Pull request comments\n")
	if len(prs) > 0 {
		pr := prs[0].Number
		cs, err := d.Reader.ListComments(ctx, pr, time.Time{})
		if err != nil {
			return executor.File{}, false, err
		}
		rcs, err := d.Reader.ListReviewComments(ctx, pr)
		if err != nil {
			return executor.File{}, false, err
		}
		for _, c := range cs {
			if !strings.EqualFold(c.Author, id.Login) {
				fmt.Fprintf(&b, "\n#### %s at %s\n\n%s\n", c.Author, c.CreatedAt.Format(time.RFC3339), c.Body)
			}
		}
		for _, c := range rcs {
			if !strings.EqualFold(c.Author, id.Login) {
				fmt.Fprintf(&b, "\n#### %s at %s, %s:%d\n\n%s\n", c.Author, c.CreatedAt.Format(time.RFC3339), c.Path, c.Line, c.Body)
			}
		}
	}
	out := b.String()
	if len(out) > feedbackCap {
		out = out[:feedbackCap] + "\n[truncated]\n"
	}
	return executor.File{Path: agentjob.InputDir(n) + "/review-feedback.md", Data: []byte(out)}, true, nil
}
