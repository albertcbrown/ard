package stale

import (
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

func ev(name, actor string, ago time.Duration) github.TimelineEvent {
	return github.TimelineEvent{Event: name, Actor: actor, CreatedAt: t0.Add(-ago)}
}

func pr(state github.State, actor string) github.TimelineEvent {
	e := ev("cross-referenced", actor, 35*day)
	e.Source = &github.IssueRef{Number: 9, IsPR: true, State: state}
	return e
}

func TestDecide(t *testing.T) {
	mod := func(f func(*github.Issue)) github.Issue { is := old(); f(&is); return is }
	tests := []struct {
		name string
		is   github.Issue
		tl   []github.TimelineEvent
		in   func(*inputs)
		want reason
	}{
		{"eligible, no events", old(), nil, nil, reasonClose},
		{"hold", old(labels.Hold), nil, nil, reasonHold},
		{"needs-human", old(labels.NeedsHuman), nil, nil, reasonNeedsHuman},
		{"running triage", old(labels.Running(labels.StageTriage, "lead")), nil, nil, reasonRunning},
		{"running worker", old(labels.Running(labels.StageWorker, "engineer")), nil, nil, reasonRunning},
		{"running review", old(labels.Running(labels.StageReview, "eng-review")), nil, nil, reasonRunning},
		{"running, unparsed label", old(labels.Prefix + "merger:x:" + string(labels.StateRunning)), nil, nil, reasonRunning},
		{"human author", mod(func(i *github.Issue) { i.Author = "alice" }), nil, nil, reasonHumanAuthor},
		{"other bot author", mod(func(i *github.Issue) { i.Author = "dependabot[bot]" }), nil, nil, reasonHumanAuthor},
		{"scanner label only", mod(func(i *github.Issue) { i.Labels = []string{labels.Scanner("cve")} }), nil, nil, reasonClose},
		{"scanner label only, human author", mod(func(i *github.Issue) { i.Labels = []string{labels.Scanner("cve")}; i.Author = "alice" }), nil, nil, reasonHumanAuthor},
		{"scanner label only, needs-human", mod(func(i *github.Issue) { i.Labels = []string{labels.Scanner("cve"), labels.NeedsHuman} }), nil, nil, reasonNotCand},
		{"scanner label only, hold", mod(func(i *github.Issue) { i.Labels = []string{labels.Scanner("cve"), labels.Hold} }), nil, nil, reasonNotCand},
		{"scanner label only, running", mod(func(i *github.Issue) {
			i.Labels = []string{labels.Scanner("cve"), labels.Running(labels.StageTriage, "lead")}
		}), nil, nil, reasonNotCand},
		{"scanner label plus other ard label", mod(func(i *github.Issue) { i.Labels = []string{labels.Scanner("cve"), labels.Prefix + "other"} }), nil, nil, reasonNotCand},
		{"scanner label plus priority label", mod(func(i *github.Issue) { i.Labels = []string{labels.Scanner("cve"), labels.P0} }), nil, nil, reasonClose},
		{"pending plus priority label", old(labels.P1), nil, nil, reasonClose},
		{"priority label only", mod(func(i *github.Issue) { i.Labels = []string{labels.P0} }), nil, nil, reasonNotCand},
		{"scanner label plus non-ard label", mod(func(i *github.Issue) { i.Labels = []string{labels.Scanner("cve"), "bug"} }), nil, nil, reasonClose},
		{"no ard label at all", mod(func(i *github.Issue) { i.Labels = []string{"bug"} }), nil, nil, reasonNotCand},
		{"no scanner label", mod(func(i *github.Issue) { i.Labels = []string{labels.Pending} }), nil, nil, reasonNotCand},
		{"undiscovered scanner", mod(func(i *github.Issue) { i.Labels = []string{labels.Pending, labels.Scanner("gone")} }), nil, nil, reasonNotCand},
		{"pull request", mod(func(i *github.Issue) { i.IsPR = true }), nil, nil, reasonNotCand},
		{"closed", mod(func(i *github.Issue) { i.State = github.StateClosed }), nil, nil, reasonNotCand},
		{"created 29d ago", mod(func(i *github.Issue) { i.CreatedAt = t0.Add(-29 * day) }), nil, nil, reasonTooNew},
		{"custom inactive_for 7d", mod(func(i *github.Issue) { i.CreatedAt = t0.Add(-8 * day) }), nil, func(in *inputs) { in.inactiveFor = 7 * day }, reasonClose},
		{"open PR by human", old(), []github.TimelineEvent{pr(github.StateOpen, "alice")}, nil, reasonLinkedPR},
		{"open PR by bot", old(), []github.TimelineEvent{pr(github.StateOpen, bot)}, nil, reasonLinkedPR},
		{"closed PR", old(), []github.TimelineEvent{pr(github.StateClosed, "alice")}, nil, reasonClose},
		{"merged PR", old(), []github.TimelineEvent{pr(github.StateClosed, bot)}, nil, reasonClose},
		{"referenced from issue", old(), []github.TimelineEvent{{Event: "cross-referenced", Actor: "a", CreatedAt: t0.Add(-35 * day), Source: &github.IssueRef{IsPR: false, State: github.StateOpen}}}, nil, reasonClose},
		{"connected alone", old(), []github.TimelineEvent{ev("connected", "alice", 35*day)}, nil, reasonLinkedPR},
		{"connected then disconnected", old(), []github.TimelineEvent{ev("connected", "alice", 38*day), ev("disconnected", "alice", 36*day)}, nil, reasonClose},
		{"human comment 10d ago", old(), []github.TimelineEvent{ev("commented", "alice", 10*day)}, nil, reasonActive},
		{"bot comment 1d ago", old(), []github.TimelineEvent{ev("commented", bot, day)}, nil, reasonClose},
		{"bot labeled 1d ago", old(), []github.TimelineEvent{ev("labeled", bot, day)}, nil, reasonClose},
		{"human labeled 5d ago", old(), []github.TimelineEvent{ev("labeled", "alice", 5*day)}, nil, reasonActive},
		{"ghost actor 1d ago", old(), []github.TimelineEvent{ev("commented", "", day)}, nil, reasonActive},
		{"exactly 30d", old(), []github.TimelineEvent{ev("commented", "alice", 30*day)}, nil, reasonClose},
		{"30d minus 1s", old(), []github.TimelineEvent{ev("commented", "alice", 30*day-time.Second)}, nil, reasonActive},
		{"non-activity event ignored", old(), []github.TimelineEvent{ev("closed", "alice", day)}, nil, reasonClose},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			i := in()
			if tc.in != nil {
				tc.in(&i)
			}
			if got, _ := decide(tc.is, tc.tl, i); got != tc.want {
				t.Errorf("decide = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLastActivityFallsBackToCreation(t *testing.T) {
	is := old()
	if got := lastActivity(is, nil, bot); !got.Equal(is.CreatedAt) {
		t.Errorf("got %v", got)
	}
}

func TestPriorStaleComment(t *testing.T) {
	last := t0.Add(-35 * day)
	marker := func(actor string, ago time.Duration) github.TimelineEvent {
		e := ev("commented", actor, ago)
		e.Body = Marker + "\nx"
		return e
	}
	tests := []struct {
		name string
		tl   []github.TimelineEvent
		want bool
	}{
		{"after last activity", []github.TimelineEvent{marker(bot, 34*day)}, true},
		{"before a reopen", []github.TimelineEvent{marker(bot, 36*day)}, false},
		{"non-bot actor", []github.TimelineEvent{marker("alice", 34*day)}, false},
		{"no marker", []github.TimelineEvent{ev("commented", bot, 34*day)}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := priorStaleComment(tc.tl, bot, last); got != tc.want {
				t.Errorf("got %v", got)
			}
		})
	}
}
