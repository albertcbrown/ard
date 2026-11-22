package reviewer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/reaper"
	"github.com/albertcbrown/ard/internal/state"
)

// pinned is what the pin step learns about the PR's head.
type pinned struct {
	tip, base string
	patch     []byte
	truncated bool
	added     map[string][]gitx.LineRange
}

// pin fetches the PR's head branch into the controller's clone and requires
// the tip to be the head GitHub reported. Nothing is claimed yet.
func (l *Loop) pin(ctx context.Context, cfg *config.Resolved, c candidate) (p pinned, step string, err error) {
	d := l.d
	spec := "+refs/heads/" + c.pr.HeadRef + ":" + localRef(c.pr.Number)
	if err := d.Git.FetchRef(ctx, d.RepoDir, d.RemoteURL, spec, d.Token); err != nil {
		return p, "fetch", err
	}
	if p.tip, err = d.Git.RevParse(ctx, d.RepoDir, localRef(c.pr.Number)); err != nil {
		return p, "rev_parse", err
	}
	if p.tip != c.pr.HeadSHA {
		return p, "head_moved", fmt.Errorf("branch is at %.12s, the PR head was %.12s", p.tip, c.pr.HeadSHA)
	}
	if p.base, err = d.Git.MergeBase(ctx, d.RepoDir, cfg.SHA, p.tip); err != nil {
		return p, "merge_base", err
	}
	if p.patch, p.truncated, err = d.Git.Diff(ctx, d.RepoDir, p.base, p.tip, maxDiffBytes, ".ard/"); err != nil {
		return p, "diff", err
	}
	if p.added, err = d.Git.AddedLinesBetween(ctx, d.RepoDir, p.base, p.tip, ".ard/"); err != nil {
		return p, "added_lines", err
	}
	return p, "", nil
}

// run claims one granted candidate and watches the job to the end, following
// the contract in package reaper. It runs in an Inflight goroutine and holds
// the pool slot until FinishWith has returned.
func (l *Loop) run(ctx context.Context, env controller.Env, cfg *config.Resolved, c candidate, slot *pool.Slot) {
	defer slot.Release()
	d := l.d
	n, pr, role := c.issue.Number, c.pr.Number, c.role
	log := d.Log
	if env.Log != nil {
		log = env.Log
	}
	log = log.With("pr", pr, "issue", n, "role", role.Name)

	// 1. The pin and the inputs. Nothing is claimed yet.
	p, step, err := l.pin(ctx, cfg, c)
	if err != nil {
		if step == "head_moved" {
			log.Info("review.head_moved", "detail", err.Error())
		} else {
			log.Error("review.claim_failed", "step", step, "error", err)
		}
		return
	}
	comments, err := d.Reader.ListComments(ctx, n, time.Time{})
	if err != nil {
		log.Error("review.claim_failed", "step", "list_comments", "error", err)
		return
	}
	dir := agentjob.InputDir(n)
	inputs := []executor.File{
		{Path: dir + "/diff.patch", Data: diffInput(pr, c.pr.HeadRef, p.base, p.tip, p.patch, p.truncated)},
		{Path: dir + "/issue.md", Data: agentjob.Snapshot(c.issue)},
		{Path: dir + "/comments.md", Data: agentjob.Comments(comments)},
	}
	// The committed .ard/<n> holds the plan and the earlier role files and logs.
	untrusted := []string{dir + "/diff.patch", dir + "/issue.md", dir + "/comments.md", fmt.Sprintf(".ard/%d", n)}

	// 2. The claim record.
	data, _ := json.Marshal(map[string]string{"head": p.tip, "base": p.base})
	sj, err := d.Store.CreateJob(ctx, state.NewJob{Stage: stageReview, Role: role.Name, Issue: n, PR: pr, Branch: c.pr.HeadRef, Data: data})
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			return
		}
		log.Error("review.claim_failed", "step", "create_job", "error", err)
		return
	}
	slot.SetJobID(sj.ID)
	log = log.With("job", sj.ID)

	// 3. The running label, on the PR. If it fails nothing has started.
	running := labels.Running(labels.StageReview, role.Name)
	if err := d.Effects.AddLabels(ctx, pr, running); err != nil {
		bg := context.WithoutCancel(ctx)
		if rerr := d.Effects.RemoveLabel(bg, pr, running); rerr != nil {
			log.Warn("review.unlabel_failed", "error", rerr)
		}
		log.Error("review.claim_failed", "step", "add_running_label", "error", err)
		if ferr := d.Store.Fail(bg, sj.ID, "claim: add running label failed", d.Now()); ferr != nil {
			log.Error("review.fail_record_failed", "error", ferr)
		}
		return
	}
	log.Info("review.claim", "head", p.tip)

	rc := &closeout.ReviewContext{MergeBase: p.base, Added: p.added}
	finish := func(h executor.Handle, st executor.Status, res agent.Result, cause closeout.Cause) {
		out, err := d.Finisher.FinishWith(ctx, sj.ID, h, st, res, reaper.FinishOptions{Cause: cause, Review: rc})
		if err != nil {
			// ErrBusy or any other error leaves a resumable close-out for the Reaper.
			log.Error("review.closeout_failed", "error", err)
			return
		}
		log.Info("review.closeout", "outcome", string(out))
	}
	cause := func(kind closeout.CauseKind, detail string, retry bool) closeout.Cause {
		return reaper.NewCause(sj, kind, detail, retry)
	}

	// 4. Start. Exactly one attempt.
	if _, err := d.Store.StartAttempt(ctx, sj.ID); err != nil {
		log.Error("review.start_failed", "step", "start_attempt", "error", err)
		finish(executor.Handle{}, executor.Status{}, agent.Result{}, cause(closeout.CauseNoProcess, "could not start the job", false))
		return
	}
	ej := agentjob.ExecJob(cfg, role, executor.Job{
		ID: sj.ID, Repo: d.Repo.String(), Issue: n, Stage: stageReview, Role: role.Name,
		RepoDir: d.RepoDir, Ref: p.tip, Inputs: inputs,
		Scrub: []string{agent.RoleFilePath(n, stageReview, role.Name)},
	})
	h, err := d.Executor.Prepare(ctx, ej)
	if err != nil {
		log.Error("review.prepare_failed", "error", err)
		if errors.Is(err, executor.ErrClone) {
			finish(executor.Handle{}, executor.Status{}, agent.Result{}, cause(closeout.CauseCloneFail, "could not clone the repository", true))
		} else {
			finish(executor.Handle{}, executor.Status{}, agent.Result{}, cause(closeout.CauseNoProcess, "could not prepare the job", false))
		}
		return
	}
	agentjob.SaveHandle(ctx, d.Store, log, sj.ID, h)
	cmd, err := agentjob.AgentCommand(d.Agents, cfg, role, agent.StageReview, n, 0, untrusted, h, d.Model)
	if err != nil {
		log.Error("review.command_failed", "error", err)
		finish(h, executor.Status{}, agent.Result{}, cause(closeout.CauseNoProcess, "could not start the agent", false))
		return
	}
	if h, err = d.Executor.Start(ctx, h, cmd); err != nil {
		log.Error("review.start_failed", "step", "start", "error", err)
		finish(h, executor.Status{}, agent.Result{}, cause(closeout.CauseNoProcess, "could not start the agent", false))
		return
	}
	agentjob.SaveHandle(ctx, d.Store, log, sj.ID, h)

	// 5. Wait. Shutdown leaves the agent running: after a restart pool
	// adoption and the Reaper own the job.
	st, err := agentjob.Wait(ctx, d.Executor, h, d.Poll, env.Stopping)
	if err != nil {
		log.Warn("review.watch_ended", "error", err)
		return
	}
	var res agent.Result
	if d.Parse != nil {
		var perr error
		if res, perr = d.Parse(ctx, cfg, sj, h, st.ExitCode); perr != nil {
			log.Warn("review.parse_failed", "error", perr)
		}
	}
	rc.HeadMoved = l.headMoved(ctx, log, c, p.tip)
	finish(h, st, res, closeout.Cause{})
}

// headMoved reports whether the PR's branch gained code (anything outside
// .ard/) since the commit the review pinned. Commits that only add .ard/ files
// (another reviewer's close-out) do not count. When the branch cannot be read
// the answer is yes: the review then runs again rather than pass a head it
// may not cover.
func (l *Loop) headMoved(ctx context.Context, log *slog.Logger, c candidate, tip string) bool {
	d := l.d
	spec := "+refs/heads/" + c.pr.HeadRef + ":" + localRef(c.pr.Number)
	if err := d.Git.FetchRef(ctx, d.RepoDir, d.RemoteURL, spec, d.Token); err != nil {
		log.Warn("review.head_check_failed", "step", "fetch", "error", err)
		return true
	}
	now, err := d.Git.RevParse(ctx, d.RepoDir, localRef(c.pr.Number))
	if err != nil {
		log.Warn("review.head_check_failed", "step", "rev_parse", "error", err)
		return true
	}
	if now == tip {
		return false
	}
	changed, err := d.Git.CodeChanged(ctx, d.RepoDir, tip, now, ".ard/")
	if err != nil {
		log.Warn("review.head_check_failed", "step", "diff", "error", err)
		return true
	}
	if len(changed) > 0 {
		log.Warn("review.head_moved_during_review", "from", tip, "to", now, "files", len(changed))
		return true
	}
	return false
}
