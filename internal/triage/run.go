package triage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/reaper"
	"github.com/albertcbrown/ard/internal/state"
)

// inputDir is where the untrusted snapshot goes, relative to the worktree. It
// is outside .ard/<n>/, the only path close-out collects, so it is never
// committed.
func inputDir(n int) string { return fmt.Sprintf(".ard-input/%d", n) }

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
	inputs := []executor.File{{Path: inputDir(n) + "/issue.md", Data: Snapshot(c.issue)}}
	untrusted := []string{inputDir(n) + "/issue.md"}
	if len(comments) > 0 {
		inputs = append(inputs, executor.File{Path: inputDir(n) + "/comments.md", Data: Comments(comments)})
		untrusted = append(untrusted, inputDir(n)+"/comments.md")
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
	ej := executor.Job{
		ID: job.ID, Repo: d.Repo, Issue: n, Stage: stageTriage, Role: role.Name,
		RepoDir: d.RepoDir, Ref: cfg.SHA, RoleDir: filepath.Join(cfg.Repo, filepath.FromSlash(role.Dir)),
		Inputs: inputs, Scrub: []string{agent.RoleFilePath(n, stageTriage, role.Name)}, Timeout: role.Timeout,
	}
	if role.Dockerfile != "" {
		ej.Dockerfile = filepath.Join(cfg.Repo, filepath.FromSlash(role.Dockerfile))
	}
	if sc, ok := cfg.SidecarFor(role); ok {
		ej.Sidecar = executor.Sidecar{Name: role.Sidecar, Repo: sc.Repo, Ref: sc.Ref}
	}
	h, err := d.Executor.Prepare(ctx, ej)
	if err != nil {
		log.Error("triage.prepare_failed", "error", err)
		if errors.Is(err, executor.ErrClone) {
			finish(executor.Handle{}, executor.Status{}, agent.Result{}, transient(closeout.CauseCloneFail, "could not clone the repository", true))
		} else {
			finish(executor.Handle{}, executor.Status{}, agent.Result{}, transient(closeout.CauseNoProcess, "could not prepare the job", false))
		}
		return
	}
	l.saveHandle(ctx, log, job.ID, h)

	cmd, err := l.command(cfg, role, n, untrusted, h)
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
	l.saveHandle(ctx, log, job.ID, h)

	// 6. Wait. Shutdown leaves the agent running: after a restart pool-adopt
	// and the Reaper own the job.
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-env.Stopping:
			cancel()
		case <-wctx.Done():
		}
	}()
	st, err := executor.WaitFor(wctx, d.Executor, h, d.Poll)
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

func (l *Loop) saveHandle(ctx context.Context, log *slog.Logger, id string, h executor.Handle) {
	b, err := json.Marshal(h)
	if err == nil {
		err = l.d.Store.SetHandle(ctx, id, b)
	}
	if err != nil {
		log.Error("triage.handle_not_saved", "error", err)
	}
}

// command builds the agent invocation for a prepared job. The prompt and the
// untrusted paths are paths only; the adapter never reads them.
func (l *Loop) command(cfg *config.Resolved, role config.Role, n int, untrusted []string, h executor.Handle) (executor.Command, error) {
	a, err := l.d.Agents.Get(role.Agent)
	if err != nil {
		return executor.Command{}, err
	}
	budget := role.MaxBudgetUSD
	if budget == 0 {
		budget = cfg.MaxBudgetUSD
	}
	spec := agent.Spec{
		Stage: agent.StageTriage, Role: role.Name, Issue: n,
		PromptFile: h.View.Role + "/prompt.txt", Untrusted: untrusted,
		ConfigDir: h.View.ConfigDir, Model: role.Model, MaxBudgetUSD: budget, Auth: l.d.Model.Auth,
	}
	if h.View.Sidecar != "" {
		spec.ReadDirs = []string{h.View.Sidecar}
	}
	ac, err := a.Command(spec)
	if err != nil {
		return executor.Command{}, err
	}
	return Command(ac, l.d.Model)
}
