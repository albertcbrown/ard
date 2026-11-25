package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/state"
)

// prBackoff is the wait before each retry of CreatePR: four attempts in all.
var prBackoff = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second}

// sleep waits d, or until ctx is done.
func (jb *job) sleep(ctx context.Context, d time.Duration) error {
	if f := jb.l.d.Sleep; f != nil {
		f(ctx, d)
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// createWithRetry opens the PR, retrying a transient failure with backoff.
// Before each retry it looks for a PR on the head, in case the failed create
// reached GitHub. Under dry-run it never retries and never sleeps.
func (jb *job) createWithRetry(ctx context.Context, in github.NewPR) (github.PullRequest, error) {
	d := jb.l.d
	pr, err := d.Effects.CreatePR(ctx, in)
	for i := 0; err != nil && github.IsTransient(err) && i < len(prBackoff); i++ {
		jb.log.Warn("worker.pr_retry", "attempt", i+1, "wait", prBackoff[i].String(), "error", err)
		if d.Effects.DryRun() {
			return pr, err
		}
		if serr := jb.sleep(ctx, prBackoff[i]); serr != nil {
			return pr, serr
		}
		if prs, lerr := d.Reader.ListPRs(ctx, github.ListPRsOpts{State: github.StateOpen, Head: in.Head}); lerr == nil && len(prs) > 0 {
			return prs[0], nil
		}
		pr, err = d.Effects.CreatePR(ctx, in)
	}
	return pr, err
}

// failCreate ends the job after the PR could not be opened with the branch
// pushed at head: it records what is needed to open the PR later and finishes
// with the pr_create_failed cause.
func (jb *job) failCreate(ctx context.Context, head, title, body string, res *closeout.WorkerResult) {
	rb, _ := json.Marshal(res)
	pp := state.PendingPR{Branch: jb.branch, Head: head, Title: title, Body: body, Summary: jb.summary, Result: rb}
	if jb.summary == "" && jb.reopened != nil {
		pp.Summary = jb.reopened.Summary
	}
	if err := jb.l.d.Store.SetPendingPR(ctx, jb.sj.ID, pp); err != nil {
		jb.log.Error("worker.pending_pr_failed", "error", err)
	}
	detail := fmt.Sprintf("pushed branch %s at %s; only the pull request is missing", jb.branch, shortSHA(head))
	jb.finish(ctx, jb.final, nil, jb.cause(closeout.CausePRCreateFailed, detail, false))
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// reopenPR opens the PR of an earlier job whose branch was pushed but whose PR
// could not be created, from that job's record: no agent step and no tests.
// It reports true when it has finished the job. It reports false, and changes
// nothing, when there is no record, the branch moved, a PR exists or a lookup
// fails: the full run then proceeds as usual.
func (jb *job) reopenPR(ctx context.Context) bool {
	d := jb.l.d
	if !jb.exists || d.Effects.DryRun() {
		return false
	}
	_, rec, ok, err := d.Store.LatestPendingPR(ctx, jb.is.Number, jb.role.Name, jb.sj.ID)
	if err != nil {
		jb.log.Warn("worker.reopen_lookup_failed", "error", err)
		return false
	}
	if !ok || rec.Branch != jb.branch {
		return false
	}
	if !jb.tipIsRecorded(ctx, rec.Head) {
		jb.log.Info("worker.reopen_skipped", "reason", "branch moved")
		return false
	}
	prs, err := d.Reader.ListPRs(ctx, github.ListPRsOpts{State: github.StateOpen, Head: jb.branch})
	if err != nil || len(prs) > 0 {
		jb.log.Info("worker.reopen_skipped", "reason", "pr exists or lookup failed", "error", err)
		return false
	}
	var res closeout.WorkerResult
	if err := json.Unmarshal(rec.Result, &res); err != nil {
		jb.log.Warn("worker.reopen_skipped", "reason", "bad record", "error", err)
		return false
	}
	jb.pushed, jb.head, jb.branch = true, rec.Head, rec.Branch
	jb.reopened = &rec
	pr, err := jb.createWithRetry(ctx, github.NewPR{Title: rec.Title, Body: rec.Body, Head: jb.branch, Base: "main", Draft: true})
	if err != nil {
		jb.log.Error("worker.pr_failed", "step", "create", "error", err)
		jb.failCreate(ctx, rec.Head, rec.Title, rec.Body, &res)
		return true
	}
	if err := d.Store.SetPR(ctx, jb.sj.ID, pr.Number); err != nil {
		jb.log.Error("worker.pr_failed", "step", "record", "error", err)
		jb.finish(ctx, step{}, nil, jb.cause(closeout.CauseNoProcess, "could not open the pull request", false))
		return true
	}
	res.PR, res.PRURL, res.Reopened, res.Summary = pr.Number, pr.URL, true, rec.Summary
	jb.log.Info("worker.pr_reopened", "pr", pr.Number, "head", rec.Head)
	jb.finish(ctx, step{}, &res, closeout.Cause{})
	return true
}

// tipIsRecorded reports whether the branch still holds the recorded head. The
// close-out of the failed job commits its log under .ard/ on top of the head,
// so a tip that only added files under .ard/ counts as unchanged.
func (jb *job) tipIsRecorded(ctx context.Context, head string) bool {
	g, dir := jb.l.d.Git, jb.l.d.RepoDir
	tip, err := g.LsRemote(ctx, dir, jb.l.d.RemoteURL, "refs/heads/"+jb.branch, jb.l.d.Token)
	if err != nil {
		return false
	}
	sha := tip["refs/heads/"+jb.branch]
	if sha == "" {
		return false
	}
	if sha == head {
		return true
	}
	ps, err := g.ChangedPaths(ctx, dir, head, sha)
	if err != nil {
		return false
	}
	for _, p := range ps {
		if p != ".ard" && !strings.HasPrefix(p, ".ard/") {
			return false
		}
	}
	return true
}
