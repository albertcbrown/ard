package merger

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/github"
)

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func run(name, status, conclusion string, id int64, done time.Duration) github.CheckRun {
	r := github.CheckRun{ID: id, Name: name, Status: status, Conclusion: conclusion}
	if status == "completed" {
		r.CompletedAt = t0.Add(done)
	}
	return r
}

func TestEvalChecks(t *testing.T) {
	req := func(names ...string) []github.RequiredCheck {
		var out []github.RequiredCheck
		for _, n := range names {
			out = append(out, github.RequiredCheck{Context: n})
		}
		return out
	}
	tests := []struct {
		name      string
		required  []github.RequiredCheck
		runs      []github.CheckRun
		graceOver bool
		want      checkState
		names     []string
	}{
		{"success", req("ci"), []github.CheckRun{run("ci", "completed", "success", 1, 0)}, false, checksGreen, nil},
		{"neutral", req("ci"), []github.CheckRun{run("ci", "completed", "neutral", 1, 0)}, false, checksGreen, nil},
		{"skipped", req("ci"), []github.CheckRun{run("ci", "completed", "skipped", 1, 0)}, false, checksGreen, nil},
		{"queued", req("ci"), []github.CheckRun{run("ci", "queued", "", 1, 0)}, false, checksPending, []string{"ci"}},
		{"in progress", req("ci"), []github.CheckRun{run("ci", "in_progress", "", 1, 0)}, false, checksPending, []string{"ci"}},
		{"missing", req("ci"), nil, true, checksPending, []string{"ci"}},
		{"failure", req("ci"), []github.CheckRun{run("ci", "completed", "failure", 1, 0)}, false, checksRed, []string{"ci"}},
		{"cancelled", req("ci"), []github.CheckRun{run("ci", "completed", "cancelled", 1, 0)}, false, checksRed, []string{"ci"}},
		{"timed out", req("ci"), []github.CheckRun{run("ci", "completed", "timed_out", 1, 0)}, false, checksRed, []string{"ci"}},
		{"action required", req("ci"), []github.CheckRun{run("ci", "completed", "action_required", 1, 0)}, false, checksRed, []string{"ci"}},
		{"red outranks pending", req("a", "b"), []github.CheckRun{run("a", "completed", "failure", 1, 0), run("b", "queued", "", 2, 0)}, false, checksRed, []string{"a"}},
		{"extra failing run ignored when a list exists", req("ci"), []github.CheckRun{run("ci", "completed", "success", 1, 0), run("other", "completed", "failure", 2, 0)}, false, checksGreen, nil},
		{"integration mismatch is missing", []github.RequiredCheck{{Context: "ci", IntegrationID: 7}},
			[]github.CheckRun{{ID: 1, Name: "ci", AppID: 8, Status: "completed", Conclusion: "success", CompletedAt: t0}}, false, checksPending, []string{"ci"}},
		{"integration match", []github.RequiredCheck{{Context: "ci", IntegrationID: 7}},
			[]github.CheckRun{{ID: 1, Name: "ci", AppID: 7, Status: "completed", Conclusion: "success", CompletedAt: t0}}, false, checksGreen, nil},
		{"duplicate names: later completion wins", req("ci"),
			[]github.CheckRun{run("ci", "completed", "failure", 9, 0), run("ci", "completed", "success", 3, time.Minute)}, false, checksGreen, nil},
		{"duplicate names: id breaks a tie", req("ci"),
			[]github.CheckRun{run("ci", "completed", "success", 3, 0), run("ci", "completed", "failure", 4, 0)}, false, checksRed, []string{"ci"}},
		{"a rerun in flight hides the old success", req("ci"),
			[]github.CheckRun{run("ci", "completed", "success", 3, 0), run("ci", "in_progress", "", 4, 0)}, false, checksPending, []string{"ci"}},

		{"no list, no runs, grace not over", nil, nil, false, checksPending, nil},
		{"no list, no runs, grace over", nil, nil, true, checksGreen, nil},
		{"no list, all green", nil, []github.CheckRun{run("a", "completed", "success", 1, 0), run("b", "completed", "skipped", 2, 0)}, false, checksGreen, nil},
		{"no list, one failing", nil, []github.CheckRun{run("a", "completed", "success", 1, 0), run("b", "completed", "failure", 2, 0)}, true, checksRed, []string{"b"}},
		{"no list, one running", nil, []github.CheckRun{run("a", "completed", "success", 1, 0), run("b", "queued", "", 2, 0)}, true, checksPending, []string{"b"}},
		{"no list, rerun fixed it", nil, []github.CheckRun{run("a", "completed", "failure", 1, 0), run("a", "completed", "success", 2, time.Minute)}, true, checksGreen, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, names := evalChecks(tc.required, tc.runs, nil, tc.graceOver)
			if got != tc.want || !reflect.DeepEqual(names, tc.names) {
				t.Fatalf("evalChecks = %v %v, want %v %v", got, names, tc.want, tc.names)
			}
		})
	}
}

func TestEvalChecksStatuses(t *testing.T) {
	ci := []github.RequiredCheck{{Context: "jenkins"}}
	st := func(state string, d time.Duration) github.CommitStatus {
		return github.CommitStatus{Context: "jenkins", State: state, UpdatedAt: t0.Add(d)}
	}
	jr := func(conclusion string, d time.Duration) github.CheckRun {
		return run("jenkins", "completed", conclusion, 1, d)
	}
	tests := []struct {
		name     string
		required []github.RequiredCheck
		runs     []github.CheckRun
		statuses []github.CommitStatus
		grace    bool
		want     checkState
		names    []string
	}{
		{"status success", ci, nil, []github.CommitStatus{st("success", 0)}, false, checksGreen, nil},
		{"status pending", ci, nil, []github.CommitStatus{st("pending", 0)}, false, checksPending, []string{"jenkins"}},
		{"status failure", ci, nil, []github.CommitStatus{st("failure", 0)}, false, checksRed, []string{"jenkins"}},
		{"status error", ci, nil, []github.CommitStatus{st("error", 0)}, false, checksRed, []string{"jenkins"}},
		{"newest status of a context wins", ci, nil, []github.CommitStatus{st("failure", 0), st("success", time.Minute)}, false, checksGreen, nil},
		{"other context ignored", ci, nil, []github.CommitStatus{{Context: "x", State: "success", UpdatedAt: t0}}, false, checksPending, []string{"jenkins"}},
		{"newer status beats older red run", ci, []github.CheckRun{jr("failure", 0)}, []github.CommitStatus{st("success", time.Minute)}, false, checksGreen, nil},
		{"newer red run beats older green status", ci, []github.CheckRun{jr("failure", time.Minute)}, []github.CommitStatus{st("success", 0)}, false, checksRed, []string{"jenkins"}},
		{"tie: red wins", ci, []github.CheckRun{jr("success", 0)}, []github.CommitStatus{st("failure", 0)}, false, checksRed, []string{"jenkins"}},
		{"run in flight beats green status", ci, []github.CheckRun{run("jenkins", "in_progress", "", 1, 0)}, []github.CommitStatus{st("success", time.Hour)}, false, checksPending, []string{"jenkins"}},
		{"pinned rule, unresolved status", []github.RequiredCheck{{Context: "jenkins", IntegrationID: 7}}, nil, []github.CommitStatus{st("success", 0)}, false, checksPending, []string{"jenkins"}},
		{"pinned rule, status from that app", []github.RequiredCheck{{Context: "jenkins", IntegrationID: 7}}, nil, []github.CommitStatus{{Context: "jenkins", State: "success", UpdatedAt: t0, AppID: 7}}, false, checksGreen, nil},
		{"pinned rule, status from another app", []github.RequiredCheck{{Context: "jenkins", IntegrationID: 7}}, nil, []github.CommitStatus{{Context: "jenkins", State: "success", UpdatedAt: t0, AppID: 8}}, false, checksPending, []string{"jenkins"}},
		{"no list, green status only", nil, nil, []github.CommitStatus{st("success", 0)}, false, checksGreen, nil},
		{"no list, pending status", nil, []github.CheckRun{run("a", "completed", "success", 2, 0)}, []github.CommitStatus{st("pending", 0)}, true, checksPending, []string{"jenkins"}},
		{"no list, red status", nil, nil, []github.CommitStatus{st("error", 0)}, true, checksRed, []string{"jenkins"}},
		{"no list, none, grace over", nil, nil, nil, true, checksGreen, nil},
		{"no list, none, grace not over", nil, nil, nil, false, checksPending, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, names := evalChecks(tc.required, tc.runs, tc.statuses, tc.grace)
			if got != tc.want || !reflect.DeepEqual(names, tc.names) {
				t.Fatalf("evalChecks = %v %v, want %v %v", got, names, tc.want, tc.names)
			}
		})
	}
}

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
