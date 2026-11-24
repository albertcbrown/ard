package merger

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/github"
)

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func TestBehind(t *testing.T) {
	for name, tc := range map[string]struct {
		changed []string
		want    bool
	}{
		"nothing":            {nil, false},
		"other issue's dir":  {[]string{".ard/12/x", ".ard/12/logs/y"}, false},
		"code":               {[]string{"internal/x.go"}, true},
		"config":             {[]string{".ard/config.yaml"}, true},
		"role directory":     {[]string{".ard/workers/eng/agent.md"}, true},
		"issue dir and code": {[]string{".ard/12/x", "a.go"}, true},
		"lookalike dir":      {[]string{".ard/12x/y"}, true},
	} {
		if got := behind(tc.changed); got != tc.want {
			t.Errorf("%s: behind(%v) = %v", name, tc.changed, got)
		}
	}
}

func TestClosingRef(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want bool
	}{
		"good":          {"Closes #7\n\nOpened by ard", true},
		"only line":     {"Closes #7", true},
		"crlf":          {"Closes #7\r\nx", true},
		"missing":       {"Opened by ard\nCloses #7", false},
		"other issue":   {"Closes #8\n", false},
		"prefix match":  {"Closes #70\n", false},
		"edited lower":  {"closes #7\n", false},
		"trailing text": {"Closes #7 and more\n", false},
		"leading space": {" Closes #7\n", false},
		"empty":         {"", false},
		"fixes":         {"Fixes #7\n", false},
	} {
		if got := closingRef(tc.body, 7); got != tc.want {
			t.Errorf("%s: closingRef = %v", name, got)
		}
	}
}

func TestApproved(t *testing.T) {
	const head = "h2"
	rv := func(id int64, author, state, sha string, at time.Duration) github.Review {
		return github.Review{ID: id, Author: author, State: state, CommitSHA: sha, SubmittedAt: t0.Add(at)}
	}
	perms := map[string]github.Permission{
		"alice": {Level: "write", Role: "write"}, "bob": {Level: "read", Role: "triage"}, "carol": {Level: "read", Role: "read"},
		"admin": {Level: "admin", Role: "admin"}, "ard[bot]": {Level: "admin", Role: "admin"},
	}
	tests := []struct {
		name    string
		reviews []github.Review
		covered map[string]bool // commit -> covers head
		want    bool
		permErr bool
	}{
		{"approval at head", []github.Review{rv(1, "alice", "APPROVED", head, 0)}, map[string]bool{head: true}, true, false},
		{"admin approves", []github.Review{rv(1, "admin", "APPROVED", head, 0)}, map[string]bool{head: true}, true, false},
		{"app approval ignored", []github.Review{rv(1, "ard[bot]", "APPROVED", head, 0)}, map[string]bool{head: true}, false, false},
		{"other bot ignored", []github.Review{rv(1, "dependabot[bot]", "APPROVED", head, 0)}, map[string]bool{head: true}, false, false},
		{"triage user is not enough", []github.Review{rv(1, "bob", "APPROVED", head, 0)}, map[string]bool{head: true}, false, false},
		{"read user is not enough", []github.Review{rv(1, "carol", "APPROVED", head, 0)}, map[string]bool{head: true}, false, false},
		{"changes requested after approval", []github.Review{rv(1, "alice", "APPROVED", head, 0), rv(2, "alice", "CHANGES_REQUESTED", head, time.Minute)}, map[string]bool{head: true}, false, false},
		{"dismissed approval", []github.Review{rv(1, "alice", "APPROVED", head, 0), rv(2, "alice", "DISMISSED", head, time.Minute)}, map[string]bool{head: true}, false, false},
		{"comment keeps approval", []github.Review{rv(1, "alice", "APPROVED", head, 0), rv(2, "alice", "COMMENTED", head, time.Minute)}, map[string]bool{head: true}, true, false},
		{"re-approval after changes", []github.Review{rv(1, "alice", "CHANGES_REQUESTED", head, 0), rv(2, "alice", "APPROVED", head, time.Minute)}, map[string]bool{head: true}, true, false},
		{"approval at old head, only main changes since", []github.Review{rv(1, "alice", "APPROVED", "old", 0)}, map[string]bool{"old": true}, true, false},
		{"approval at old head, PR code since", []github.Review{rv(1, "alice", "APPROVED", "old", 0)}, map[string]bool{"old": false}, false, false},
		{"one good approver among others", []github.Review{rv(1, "bob", "APPROVED", head, 0), rv(2, "alice", "APPROVED", head, time.Minute)}, map[string]bool{head: true}, true, false},
		{"out of order list still sorted", []github.Review{rv(2, "alice", "CHANGES_REQUESTED", head, time.Minute), rv(1, "alice", "APPROVED", head, 0)}, map[string]bool{head: true}, false, false},
		{"no reviews", nil, nil, false, false},
		{"permission error fails closed", []github.Review{rv(1, "alice", "APPROVED", head, 0)}, map[string]bool{head: true}, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := approved(context.Background(), tc.reviews, approvalCheck{
				self: "ard[bot]",
				permission: func(_ context.Context, l string) (github.Permission, error) {
					if tc.permErr {
						return github.Permission{}, errors.New("boom")
					}
					return perms[l], nil
				},
				covers: func(_ context.Context, at string) (bool, error) { return tc.covered[at], nil },
			})
			if (err != nil) != tc.permErr || got != tc.want {
				t.Fatalf("approved = %v, %v; want %v (err %v)", got, err, tc.want, tc.permErr)
			}
		})
	}
}
