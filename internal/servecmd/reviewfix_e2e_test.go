package servecmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/reviewfix"
	"github.com/albertcbrown/ard/internal/state"
)

// A reviewer that asks for changes while hello.go ignores its argument and
// approves once it uses it.
const switchingReviewer = `#!/bin/sh
for d in .ard-input/*; do n=${d##*/}; done
mkdir -p ".ard/$n"
if grep -q 'return "hi"' hello.go; then
cat > ".ard/$n/review-eng-review.md" <<'EOF2'
---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: hello.go
    line: 2
    severity: high
    blocking: true
    message: The function ignores its argument.
---
One blocking problem.
EOF2
else
cat > ".ard/$n/review-eng-review.md" <<'EOF2'
---
role: eng-review
status: done
attempt: 1
verdict: approve
findings: []
---
Looks good.
EOF2
fi
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"duration_ms":10,"num_turns":1,"total_cost_usd":0.01,"session_id":"s","result":"ok"}'
`

type reviewfixE2E struct {
	*reviewE2E
	fix controller.Loop
}

func newReviewfixE2E(t *testing.T, script string, maxRounds int) *reviewfixE2E {
	t.Helper()
	e := newReviewE2E(t, script)
	e.cfg.Roles = append(e.cfg.Roles, config.Role{Kind: config.KindWorker, Name: "engineer", MaxReviewRounds: maxRounds})
	ctx := context.Background()
	if err := e.gh.AddLabels(ctx, e.issueN, labels.Request(labels.StageWorker, "engineer"), labels.Done(labels.StageWorker, "engineer")); err != nil {
		t.Fatal(err)
	}
	return &reviewfixE2E{reviewE2E: e, fix: reviewfixLoop(e.w)}
}

func (e *reviewfixE2E) tickFix() {
	e.t.Helper()
	ctx := context.Background()
	if err := e.fix.Tick(ctx, controller.Env{Cfg: e.cfg, Log: e.w.Log, Inflight: controller.NewInflight(ctx, e.w.Log)}); err != nil {
		e.t.Fatalf("reviewfix tick: %v", err)
	}
}

// review runs the reviewer loop, then moves the fake PR's head to the branch
// tip, as GitHub does when close-out pushes.
func (e *reviewfixE2E) review() {
	e.t.Helper()
	e.tick()
	e.syncHead()
}

func (e *reviewfixE2E) syncHead() {
	e.t.Helper()
	e.gh.SetHeadSHA(e.prN, strings.TrimSpace(gittest.Run(e.t, e.bare, "rev-parse", e.branch)))
}

// worker plays the worker loop: a fix commit and its close-out file on the
// branch, a completed worker job and the :done label.
func (e *reviewfixE2E) worker() {
	e.t.Helper()
	ctx := context.Background()
	dir := e.t.TempDir()
	gittest.Run(e.t, dir, "clone", "-q", "-b", e.branch, e.bare, ".")
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	write("hello.go", "package main\nfunc Hello(name string) string {\n\treturn \"hi \" + name\n}\n")
	gittest.Run(e.t, dir, "add", "-A")
	gittest.Run(e.t, dir, "-c", "user.name=w", "-c", "user.email=w@example.com", "commit", "-q", "-m", "fix: use the argument")
	write(".ard/"+itoa(e.issueN)+"/worker-engineer.md", "---\nrole: engineer\nstatus: done\nattempt: 1\n---\nFixed.\n")
	gittest.Run(e.t, dir, "add", "-A")
	gittest.Run(e.t, dir, "-c", "user.name=w", "-c", "user.email=w@example.com", "commit", "-q", "-m", "ard(worker:engineer): fixed")
	gittest.Run(e.t, dir, "push", "-q", "origin", "HEAD:refs/heads/"+e.branch)
	e.syncHead()
	j, err := e.w.Store.CreateJob(ctx, state.NewJob{Stage: "worker", Role: "engineer", Issue: e.issueN, Branch: e.branch})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.w.Store.Complete(ctx, j.ID, nil); err != nil {
		e.t.Fatal(err)
	}
	if err := e.gh.AddLabels(ctx, e.issueN, labels.Done(labels.StageWorker, "engineer")); err != nil {
		e.t.Fatal(err)
	}
}

func (e *reviewfixE2E) issueHas(label string) bool {
	e.t.Helper()
	is, err := e.gh.GetIssue(context.Background(), e.issueN)
	if err != nil {
		e.t.Fatal(err)
	}
	return has(is.Labels, label)
}

func (e *reviewfixE2E) rounds() string {
	e.t.Helper()
	rs, err := reviewfix.Rounds(context.Background(), e.w.Store, e.issueN)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, r := range rs {
		out = append(out, r.Phase)
	}
	return strings.Join(out, ",")
}

// changes -> fix -> approve.
func TestReviewFixRoundEndToEnd(t *testing.T) {
	e := newReviewfixE2E(t, switchingReviewer, 3)
	eng := "eng-review"

	e.review()
	if ls := e.prLabels(); !has(ls, labels.Changes(eng)) {
		t.Fatalf("PR labels after review = %v", ls)
	}
	e.tickFix()
	if got := e.rounds(); got != "requested" || e.issueHas(labels.Done(labels.StageWorker, "engineer")) {
		t.Fatalf("after the first tick: rounds %q, worker done label %v", got, e.issueHas(labels.Done(labels.StageWorker, "engineer")))
	}
	e.tickFix() // the worker has not run: nothing changes
	if got := e.rounds(); got != "requested" {
		t.Fatalf("rounds = %q", got)
	}

	e.worker()
	e.tickFix()
	if got := e.rounds(); got != "requested,applied" {
		t.Fatalf("rounds = %q", got)
	}
	if ls := e.prLabels(); has(ls, labels.Changes(eng)) || has(ls, labels.Done(labels.StageReview, eng)) || !has(ls, labels.Request(labels.StageReview, eng)) {
		t.Fatalf("PR labels after the fix = %v", ls)
	}

	e.review()
	e.review() // the second tick marks the approved PR ready
	if ls := e.prLabels(); !has(ls, labels.Done(labels.StageReview, eng)) || has(ls, labels.Changes(eng)) {
		t.Fatalf("PR labels after approval = %v", ls)
	}
	prs, _ := e.gh.ListPRs(context.Background(), github.ListPRsOpts{State: github.StateOpen})
	if len(prs) != 1 || prs[0].Draft {
		t.Errorf("the PR is still a draft: %+v", prs)
	}
	e.tickFix()
	rs, _ := reviewfix.Rounds(context.Background(), e.w.Store, e.issueN)
	if reviewfix.Requested(rs) != 1 || len(rs) != 2 {
		t.Errorf("rounds = %+v, want one requested and one applied", rs)
	}
}

// A reviewer that never approves stops at the cap.
func TestReviewFixCapEndToEnd(t *testing.T) {
	e := newReviewfixE2E(t, reviewerAgent, 1)
	ctx := context.Background()

	e.review()
	e.tickFix()
	e.worker()
	e.tickFix()
	e.review() // changes again
	if got := e.rounds(); got != "requested,applied" {
		t.Fatalf("rounds = %q", got)
	}
	e.tickFix()
	if got := e.rounds(); got != "requested,applied,capped" {
		t.Fatalf("rounds = %q", got)
	}
	cs, err := e.gh.ListComments(ctx, e.issueN, time.Time{})
	if err != nil || len(cs) != 1 || !strings.HasPrefix(cs[0].Body, "<!-- ard:review-rounds head=") {
		t.Fatalf("comments = %+v, %v", cs, err)
	}
	if !e.issueHas(labels.NeedsHuman) {
		t.Error("the issue was not sent to a human")
	}
	e.tickFix()
	e.tickFix()
	if cs, _ := e.gh.ListComments(ctx, e.issueN, time.Time{}); len(cs) != 1 {
		t.Errorf("comments after more ticks = %d, want 1", len(cs))
	}
}
