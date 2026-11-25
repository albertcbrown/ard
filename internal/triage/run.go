package triage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/reaper"
	"github.com/albertcbrown/ard/internal/state"
)

// run claims one granted candidate and watches the job to the end, following
// the contract in package reaper. It runs in an Inflight goroutine and holds
// the pool slot until Finish has returned.
func (l *Loop) run(ctx context.Context, env controller.Env, cfg *config.Resolved, c candidate, slot *pool.Slot) {
	defer slot.Release()
	d := l.d
	n, role := c.issue.Number, c.role
	log := l.d.Log.With("issue", n, "role", role.Name)
	if env.Log != nil {
		log = env.Log.With("issue", n, "role", role.Name)
	}

	// 1. Read what the agent will see. Nothing is claimed yet.
	comments, err := d.Reader.ListComments(ctx, n, time.Time{})
	if err != nil {
		log.Error("triage.claim_failed", "step", "list_comments", "error", err)
		return
	}
	inputs := []executor.File{{Path: agentjob.InputDir(n) + "/issue.md", Data: agentjob.Snapshot(c.issue)}}
	untrusted := []string{agentjob.InputDir(n) + "/issue.md"}
	inputs = append(inputs, executor.File{Path: agentjob.InputDir(n) + "/comments.md", Data: agentjob.Comments(comments)})
	untrusted = append(untrusted, agentjob.InputDir(n)+"/comments.md")
	// A bounced worker's network request, from the close-out record only.
	if needs, ok, err := closeout.NetworkRequest(ctx, d.Store, n); err != nil {
		log.Warn("triage.network_request_unreadable", "error", err)
	} else if ok {
		path := agentjob.InputDir(n) + "/network-request.md"
		inputs = append(inputs, executor.File{Path: path, Data: closeout.RenderNetworkRequest(needs)})
		untrusted = append(untrusted, path)
	}

	// 2. The claim record.
	data, _ := json.Marshal(map[string]string{"admission_reason": string(c.reason), "labeled_by": labeledBy(c)})
	job, err := d.Store.CreateJob(ctx, state.NewJob{Stage: stageTriage, Role: role.Name, Issue: n, Data: data})
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			return
		}
		log.Error("triage.claim_failed", "step", "create_job", "error", err)
		return
	}
	slot.SetJobID(job.ID)
	log = log.With("job", job.ID)
	failClaim := func(why string) {
		if ferr := d.Store.Fail(context.WithoutCancel(ctx), job.ID, why, d.Now()); ferr != nil {
			log.Error("triage.fail_record_failed", "error", ferr)
		}
	}

	// 3. The admission of a human-filed issue, with the lead job that used it.
	if c.rec != nil {
		c.rec.JobID = job.ID
		if err := d.Store.SetAdmission(ctx, *c.rec); err != nil {
			log.Error("triage.claim_failed", "step", "record_admission", "error", err)
			failClaim("claim: record admission failed")
			return
		}
		data, _ := json.Marshal(map[string]string{"outcome": "admit", "reason": c.rec.Reason, "actor": c.rec.LabeledBy, "action": "admit"})
		if err := d.Store.AppendEvent(ctx, state.Event{Kind: state.EventIntake, JobID: job.ID, Issue: n, Stage: stageTriage, Role: role.Name, Data: data}); err != nil {
			log.Warn("triage.event_failed", "error", err)
		}
	}

	// 4. The running label. If it fails nothing has started, so no close-out.
	running := labels.Running(labels.StageTriage, role.Name)
	if err := d.Effects.AddLabels(ctx, n, running); err != nil {
		bg := context.WithoutCancel(ctx)
		if rerr := d.Effects.RemoveLabel(bg, n, running); rerr != nil {
			log.Warn("triage.unlabel_failed", "error", rerr)
		}
		log.Error("triage.claim_failed", "step", "add_running_label", "error", err)
		failClaim("claim: add running label failed")
		return
	}
	log.Info("triage.claim", "reason", string(c.reason))

	finish := func(h executor.Handle, st executor.Status, res agent.Result, cause closeout.Cause) {
		out, err := d.Finisher.Finish(ctx, job.ID, h, st, res, cause)
		if err != nil {
			// ErrBusy or any other error leaves a resumable close-out for the Reaper.
			log.Error("triage.closeout_failed", "error", err)
			return
		}
		log.Info("triage.closeout", "outcome", string(out))
	}
	transient := func(kind closeout.CauseKind, detail string, retry bool) closeout.Cause {
		return reaper.NewCause(job, kind, detail, retry)
	}

	// 5. Start. Exactly one attempt.
	if _, err := d.Store.StartAttempt(ctx, job.ID); err != nil {
		log.Error("triage.start_failed", "step", "start_attempt", "error", err)
		finish(executor.Handle{}, executor.Status{}, agent.Result{}, transient(closeout.CauseNoProcess, "could not start the job", false))
		return
	}
	ej := agentjob.ExecJob(cfg, role, executor.Job{
		ID: job.ID, Repo: d.Repo, Issue: n, Stage: stageTriage, Role: role.Name,
		RepoDir: d.RepoDir, Ref: cfg.SHA,
		Inputs: inputs, Scrub: []string{agent.RoleFilePath(n, stageTriage, role.Name)},
	})
	h, err := d.Executor.Prepare(ctx, ej)
	if err != nil {
		log.Error("triage.prepare_failed", "error", err)
		if errors.Is(err, executor.ErrClone) {
			finish(executor.Handle{}, executor.Status{}, agent.Result{}, transient(closeout.CauseCloneFail, "could not clone the repository", true))
		} else if errors.Is(err, executor.ErrInputsTooLarge) {
			finish(executor.Handle{}, executor.Status{}, agent.Result{}, transient(closeout.CauseInputsTooLarge, executor.InputsTooLargeDetail, false))
		} else {
			finish(executor.Handle{}, executor.Status{}, agent.Result{}, transient(closeout.CauseNoProcess, "could not prepare the job", false))
		}
		return
	}
	agentjob.SaveHandle(ctx, d.Store, log, job.ID, h)

	cmd, err := agentjob.AgentCommand(d.Agents, cfg, role, agent.StageTriage, n, 0, untrusted, h, d.Model)
	if err != nil {
		log.Error("triage.command_failed", "error", err)
		finish(h, executor.Status{}, agent.Result{}, transient(closeout.CauseNoProcess, "could not start the agent", false))
		return
	}
	h, err = d.Executor.Start(ctx, h, cmd)
	if err != nil {
		log.Error("triage.start_failed", "step", "start", "error", err)
		finish(h, executor.Status{}, agent.Result{}, transient(closeout.CauseNoProcess, "could not start the agent", false))
		return
	}
	agentjob.SaveHandle(ctx, d.Store, log, job.ID, h)

	// 6. Wait. Shutdown leaves the agent running: after a restart pool-adopt
	// and the Reaper own the job.
	st, err := agentjob.Wait(ctx, d.Executor, h, d.Poll, env.Stopping)
	if err != nil {
		log.Warn("triage.watch_ended", "error", err)
		return
	}
	var res agent.Result
	if d.Parse != nil {
		var perr error
		if res, perr = d.Parse(ctx, cfg, job, h, st.ExitCode); perr != nil {
			log.Warn("triage.parse_failed", "error", perr)
		}
	}
	finish(h, st, res, closeout.Cause{})
}

func labeledBy(c candidate) string {
	if c.rec != nil {
		return c.rec.LabeledBy
	}
	return ""
}
