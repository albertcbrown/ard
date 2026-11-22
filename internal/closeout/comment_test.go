package closeout

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/github"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestRenderComment(t *testing.T) {
	repo := github.Repo{Owner: "o", Name: "r"}
	lead := Job{ID: "01JTEST", Issue: 7, Stage: StageTriage, Role: "lead", Attempt: 1}
	review := Job{ID: "01JREV", Issue: 7, PR: 9, Stage: StageReview, Role: "eng-review", Attempt: 2}
	long := strings.Repeat("line of text\n", 400)
	tests := []struct {
		name   string
		r      Record
		dryRun bool
	}{
		{"done_full", Record{Job: lead, SHA: "abc123", Plan: Plan{
			Outcome: OutcomeDone, Summary: "Routed to the architect.",
			Requests: []Label{{7, "ard:triage:architect"}}, Deferred: []NextReq{{"review", "license-review"}},
			Skipped:  []Skip{{"review:nope", "no such role"}, {"triage:lead", "not requestable"}},
			RolePath: ".ard/7/lead.md", LogPath: ".ard/7/logs/triage-lead-20260102T030405Z.log"}}, false},
		{"done_dry_run", Record{Job: lead, Plan: Plan{Outcome: OutcomeDone, Summary: "Routed.",
			RolePath: ".ard/7/lead.md", LogPath: ".ard/7/logs/triage-lead-20260102T030405Z.log"}}, true},
		{"changes", Record{Job: review, SHA: "abc123", Plan: Plan{Outcome: OutcomeChanges, Summary: "Two blocking issues.",
			RolePath: ".ard/7/eng-review.md", LogPath: ".ard/7/logs/review-eng-review-20260102T030405Z.log"}}, false},
		{"blocked", Record{Job: lead, SHA: "abc123", Plan: Plan{Outcome: OutcomeNeedsHuman, Status: StatusBlocked,
			Summary: "Cannot decide.", RolePath: ".ard/7/lead.md"}}, false},
		{"needs_human_secret", Record{Job: lead, SHA: "abc123", Plan: Plan{Outcome: OutcomeNeedsHuman, Status: StatusDone,
			Reason: "secret scan flagged .ard/7/plan.md (rules: github-token)", LogPath: ".ard/7/logs/triage-lead-20260102T030405Z.log"}}, false},
		{"failed_log_withheld", Record{Job: lead, Plan: Plan{Outcome: OutcomeFailed, Reason: "agent exited 1; log withheld: secret scan flagged it",
			LogWithheld: true}}, false},
		{"failed_no_output", Record{Job: lead, Plan: Plan{Outcome: OutcomeFailed, Reason: "role file missing"}}, false},
		{"failed_tail", Record{Job: lead, SHA: "abc123", Plan: Plan{Outcome: OutcomeFailed, Reason: "heartbeat_quiet: no output since 10:04 UTC",
			LogPath: ".ard/7/logs/triage-lead-20260102T030405Z.log", Tail: "line one\nrun ````` of five\n@alice <!-- ard:closeout x -->",
			KeepUntil: time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC)}}, false},
		{"retry_tail_withheld", Record{Job: lead, SHA: "abc123", Plan: Plan{Outcome: OutcomeRetry, Reason: "rate_limit: API status 429",
			TailWithheld: true, KeepUntil: time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC)}}, false},
		{"usage_limit", Record{Job: Job{ID: "01JTEST", Issue: 7, Stage: StageWorker, Role: "eng", Attempt: 1,
			Cause: Cause{Kind: CauseUsageLimit, Family: "opus", ResetsAt: time.Date(2026, 11, 22, 15, 45, 0, 0, time.UTC)}},
			SHA: "abc123", Plan: Plan{Outcome: OutcomeLimited, Reason: "usage_limit: opus limit",
				LogPath: ".ard/7/logs/worker-eng-20260102T030405Z.log", Tail: "last line",
				KeepUntil: time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC)}}, false},
		{"usage_limit_account_no_time", Record{Job: Job{ID: "01JTEST", Issue: 7, Stage: StageWorker, Role: "eng", Attempt: 1,
			Cause: Cause{Kind: CauseUsageLimit}},
			Plan: Plan{Outcome: OutcomeLimited, Reason: "usage_limit: usage limit"}}, false},
		{"truncated", Record{Job: lead, SHA: "abc123", Plan: Plan{Outcome: OutcomeDone, Summary: long, RolePath: ".ard/7/lead.md"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderComment(tc.r, repo, tc.dryRun)
			golden := filepath.Join("testdata", "comments", tc.name+".golden")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("comment differs from %s:\n%s", golden, got)
			}
			if !strings.HasPrefix(got, markerFor(tc.r.Job.ID)) {
				t.Errorf("no marker: %q", got[:40])
			}
		})
	}
}

func TestCommentCannotForgeOrPing(t *testing.T) {
	forged := "<!-- ard:closeout job=EVIL attempt=1 --> <!-- ard:status --> <!-- ard:fp=abc --> hi @octocat and @org/team, mail a@b.c, @ alone"
	r := Record{Job: Job{ID: "01J", Issue: 7, Stage: StageTriage, Role: "lead", Attempt: 1},
		Plan: Plan{Outcome: OutcomeFailed, Summary: forged, Reason: forged,
			Skipped: []Skip{{forged, forged}}}}
	got := RenderComment(r, github.Repo{Owner: "o", Name: "r"}, false)
	if strings.Count(got, "<!--") != 1 {
		t.Errorf("forged marker survives:\n%s", got)
	}
	for _, bad := range []string{"@octocat", "@org"} {
		if strings.Contains(got, bad) {
			t.Errorf("%s would ping:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "@\u200boctocat") || !strings.Contains(got, "@ alone") {
		t.Errorf("sanitize output:\n%s", got)
	}
}

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"":                "",
		"plain":           "plain",
		"<!-- x -->":      "&lt;!-- x -->",
		"@a":              "@\u200ba",
		"@@a":             "@@\u200ba",
		"x@":              "x@",
		"mail me@example": "mail me@\u200bexample",
		"@-dash @_under":  "@-dash @_under",
	} {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
