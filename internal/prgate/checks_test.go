package prgate

import (
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
		want      CheckState
		names     []string
	}{
		{"success", req("ci"), []github.CheckRun{run("ci", "completed", "success", 1, 0)}, false, ChecksGreen, nil},
		{"neutral", req("ci"), []github.CheckRun{run("ci", "completed", "neutral", 1, 0)}, false, ChecksGreen, nil},
		{"skipped", req("ci"), []github.CheckRun{run("ci", "completed", "skipped", 1, 0)}, false, ChecksGreen, nil},
		{"queued", req("ci"), []github.CheckRun{run("ci", "queued", "", 1, 0)}, false, ChecksPending, []string{"ci"}},
		{"in progress", req("ci"), []github.CheckRun{run("ci", "in_progress", "", 1, 0)}, false, ChecksPending, []string{"ci"}},
		{"missing", req("ci"), nil, true, ChecksPending, []string{"ci"}},
		{"failure", req("ci"), []github.CheckRun{run("ci", "completed", "failure", 1, 0)}, false, ChecksRed, []string{"ci"}},
		{"cancelled", req("ci"), []github.CheckRun{run("ci", "completed", "cancelled", 1, 0)}, false, ChecksRed, []string{"ci"}},
		{"timed out", req("ci"), []github.CheckRun{run("ci", "completed", "timed_out", 1, 0)}, false, ChecksRed, []string{"ci"}},
		{"action required", req("ci"), []github.CheckRun{run("ci", "completed", "action_required", 1, 0)}, false, ChecksRed, []string{"ci"}},
		{"red outranks pending", req("a", "b"), []github.CheckRun{run("a", "completed", "failure", 1, 0), run("b", "queued", "", 2, 0)}, false, ChecksRed, []string{"a"}},
		{"extra failing run ignored when a list exists", req("ci"), []github.CheckRun{run("ci", "completed", "success", 1, 0), run("other", "completed", "failure", 2, 0)}, false, ChecksGreen, nil},
		{"integration mismatch is missing", []github.RequiredCheck{{Context: "ci", IntegrationID: 7}},
			[]github.CheckRun{{ID: 1, Name: "ci", AppID: 8, Status: "completed", Conclusion: "success", CompletedAt: t0}}, false, ChecksPending, []string{"ci"}},
		{"integration match", []github.RequiredCheck{{Context: "ci", IntegrationID: 7}},
			[]github.CheckRun{{ID: 1, Name: "ci", AppID: 7, Status: "completed", Conclusion: "success", CompletedAt: t0}}, false, ChecksGreen, nil},
		{"duplicate names: later completion wins", req("ci"),
			[]github.CheckRun{run("ci", "completed", "failure", 9, 0), run("ci", "completed", "success", 3, time.Minute)}, false, ChecksGreen, nil},
		{"duplicate names: id breaks a tie", req("ci"),
			[]github.CheckRun{run("ci", "completed", "success", 3, 0), run("ci", "completed", "failure", 4, 0)}, false, ChecksRed, []string{"ci"}},
		{"a rerun in flight hides the old success", req("ci"),
			[]github.CheckRun{run("ci", "completed", "success", 3, 0), run("ci", "in_progress", "", 4, 0)}, false, ChecksPending, []string{"ci"}},

		{"no list, no runs, grace not over", nil, nil, false, ChecksPending, nil},
		{"no list, no runs, grace over", nil, nil, true, ChecksGreen, nil},
		{"no list, all green", nil, []github.CheckRun{run("a", "completed", "success", 1, 0), run("b", "completed", "skipped", 2, 0)}, false, ChecksGreen, nil},
		{"no list, one failing", nil, []github.CheckRun{run("a", "completed", "success", 1, 0), run("b", "completed", "failure", 2, 0)}, true, ChecksRed, []string{"b"}},
		{"no list, one running", nil, []github.CheckRun{run("a", "completed", "success", 1, 0), run("b", "queued", "", 2, 0)}, true, ChecksPending, []string{"b"}},
		{"no list, rerun fixed it", nil, []github.CheckRun{run("a", "completed", "failure", 1, 0), run("a", "completed", "success", 2, time.Minute)}, true, ChecksGreen, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, names := EvalChecks(tc.required, tc.runs, nil, tc.graceOver)
			if got != tc.want || !reflect.DeepEqual(names, tc.names) {
				t.Fatalf("EvalChecks = %v %v, want %v %v", got, names, tc.want, tc.names)
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
		want     CheckState
		names    []string
	}{
		{"status success", ci, nil, []github.CommitStatus{st("success", 0)}, false, ChecksGreen, nil},
		{"status pending", ci, nil, []github.CommitStatus{st("pending", 0)}, false, ChecksPending, []string{"jenkins"}},
		{"status failure", ci, nil, []github.CommitStatus{st("failure", 0)}, false, ChecksRed, []string{"jenkins"}},
		{"status error", ci, nil, []github.CommitStatus{st("error", 0)}, false, ChecksRed, []string{"jenkins"}},
		{"newest status of a context wins", ci, nil, []github.CommitStatus{st("failure", 0), st("success", time.Minute)}, false, ChecksGreen, nil},
		{"other context ignored", ci, nil, []github.CommitStatus{{Context: "x", State: "success", UpdatedAt: t0}}, false, ChecksPending, []string{"jenkins"}},
		{"newer status beats older red run", ci, []github.CheckRun{jr("failure", 0)}, []github.CommitStatus{st("success", time.Minute)}, false, ChecksGreen, nil},
		{"newer red run beats older green status", ci, []github.CheckRun{jr("failure", time.Minute)}, []github.CommitStatus{st("success", 0)}, false, ChecksRed, []string{"jenkins"}},
		{"tie: red wins", ci, []github.CheckRun{jr("success", 0)}, []github.CommitStatus{st("failure", 0)}, false, ChecksRed, []string{"jenkins"}},
		{"run in flight beats green status", ci, []github.CheckRun{run("jenkins", "in_progress", "", 1, 0)}, []github.CommitStatus{st("success", time.Hour)}, false, ChecksPending, []string{"jenkins"}},
		{"pinned rule, unresolved status", []github.RequiredCheck{{Context: "jenkins", IntegrationID: 7}}, nil, []github.CommitStatus{st("success", 0)}, false, ChecksPending, []string{"jenkins"}},
		{"pinned rule, status from that app", []github.RequiredCheck{{Context: "jenkins", IntegrationID: 7}}, nil, []github.CommitStatus{{Context: "jenkins", State: "success", UpdatedAt: t0, AppID: 7}}, false, ChecksGreen, nil},
		{"pinned rule, status from another app", []github.RequiredCheck{{Context: "jenkins", IntegrationID: 7}}, nil, []github.CommitStatus{{Context: "jenkins", State: "success", UpdatedAt: t0, AppID: 8}}, false, ChecksPending, []string{"jenkins"}},
		{"no list, green status only", nil, nil, []github.CommitStatus{st("success", 0)}, false, ChecksGreen, nil},
		{"no list, pending status", nil, []github.CheckRun{run("a", "completed", "success", 2, 0)}, []github.CommitStatus{st("pending", 0)}, true, ChecksPending, []string{"jenkins"}},
		{"no list, red status", nil, nil, []github.CommitStatus{st("error", 0)}, true, ChecksRed, []string{"jenkins"}},
		{"no list, none, grace over", nil, nil, nil, true, ChecksGreen, nil},
		{"no list, none, grace not over", nil, nil, nil, false, ChecksPending, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, names := EvalChecks(tc.required, tc.runs, tc.statuses, tc.grace)
			if got != tc.want || !reflect.DeepEqual(names, tc.names) {
				t.Fatalf("EvalChecks = %v %v, want %v %v", got, names, tc.want, tc.names)
			}
		})
	}
}

// #256: required contexts reported only as green commit statuses are green,
// never pending.
func TestStatusesOnlyGreenNeverPending(t *testing.T) {
	required := []github.RequiredCheck{{Context: "jenkins"}, {Context: "lint"}}
	statuses := []github.CommitStatus{
		{Context: "jenkins", State: "success", UpdatedAt: t0},
		{Context: "lint", State: "success", UpdatedAt: t0},
	}
	for _, grace := range []bool{false, true} {
		if got, names := EvalChecks(required, nil, statuses, grace); got != ChecksGreen || names != nil {
			t.Fatalf("grace=%v: EvalChecks = %v %v, want green", grace, got, names)
		}
	}
}
