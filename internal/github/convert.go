package github

import (
	gogh "github.com/google/go-github/v88/github"
)

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func login(u *gogh.User) string {
	if u == nil {
		return ""
	}
	return u.GetLogin()
}

func labelNames(ls []*gogh.Label) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.GetName())
	}
	return out
}

func issueFrom(i *gogh.Issue) Issue {
	is := Issue{
		Number:      i.GetNumber(),
		NodeID:      i.GetNodeID(),
		Title:       i.GetTitle(),
		Body:        i.GetBody(),
		State:       State(i.GetState()),
		StateReason: StateReason(i.GetStateReason()),
		Author:      login(i.User),
		Labels:      labelNames(i.Labels),
		IsPR:        i.IsPullRequest(),
		CreatedAt:   i.GetCreatedAt().Time,
		UpdatedAt:   i.GetUpdatedAt().Time,
		URL:         i.GetHTMLURL(),
	}
	if i.ClosedAt != nil {
		t := i.ClosedAt.Time
		is.ClosedAt = &t
	}
	return is
}

func labelFrom(l *gogh.Label) Label {
	return Label{Name: l.GetName(), Color: l.GetColor(), Description: l.GetDescription()}
}

func commentFrom(c *gogh.IssueComment) Comment {
	return Comment{
		ID:        c.GetID(),
		Author:    login(c.User),
		Body:      c.GetBody(),
		CreatedAt: c.GetCreatedAt().Time,
		URL:       c.GetHTMLURL(),
	}
}

func prFrom(p *gogh.PullRequest) PullRequest {
	pr := PullRequest{
		Number:         p.GetNumber(),
		NodeID:         p.GetNodeID(),
		Title:          p.GetTitle(),
		Body:           p.GetBody(),
		State:          State(p.GetState()),
		Draft:          p.GetDraft(),
		Merged:         p.GetMerged() || p.MergedAt != nil,
		Author:         login(p.User),
		Labels:         labelNames(p.Labels),
		MergeableState: p.GetMergeableState(),
		URL:            p.GetHTMLURL(),
	}
	if p.Head != nil {
		pr.HeadRef = p.Head.GetRef()
		pr.HeadSHA = p.Head.GetSHA()
	}
	if p.Base != nil {
		pr.BaseRef = p.Base.GetRef()
	}
	if p.Mergeable != nil {
		m := *p.Mergeable
		pr.Mergeable = &m
	}
	return pr
}

func prFileFrom(f *gogh.CommitFile) PRFile {
	return PRFile{
		Path:         f.GetFilename(),
		PreviousPath: f.GetPreviousFilename(),
		Status:       f.GetStatus(),
		Additions:    f.GetAdditions(),
		Deletions:    f.GetDeletions(),
	}
}

func reviewCommentFrom(c *gogh.PullRequestComment) ReviewComment {
	line := c.GetLine()
	if line == 0 {
		line = c.GetOriginalLine()
	}
	return ReviewComment{
		ID:        c.GetID(),
		Author:    login(c.User),
		Path:      c.GetPath(),
		Line:      line,
		Body:      c.GetBody(),
		CommitSHA: c.GetCommitID(),
		CreatedAt: c.GetCreatedAt().Time,
	}
}

func timelineFrom(t *gogh.Timeline) TimelineEvent {
	ev := TimelineEvent{
		Event:     t.GetEvent(),
		CreatedAt: t.GetCreatedAt().Time,
		Body:      t.GetBody(),
	}
	// "commented" events carry the author as actor in some payloads and in
	// "user" in others; the typed Timeline exposes Actor only.
	switch {
	case t.Actor != nil:
		ev.Actor = t.Actor.GetLogin()
	case t.User != nil:
		ev.Actor = t.User.GetLogin()
	}
	if t.CreatedAt == nil && t.SubmittedAt != nil {
		ev.CreatedAt = t.SubmittedAt.Time
	}
	if t.Label != nil {
		ev.Label = t.Label.GetName()
	}
	if t.Source != nil && t.Source.Issue != nil {
		is := t.Source.Issue
		ev.Source = &IssueRef{Number: is.GetNumber(), IsPR: is.IsPullRequest(), State: State(is.GetState())}
	}
	return ev
}
