package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/reaper"
	"github.com/albertcbrown/ard/internal/state"
)

const (
	testOutputBytes = 64 << 10
	defaultTimeout  = 30 * time.Minute
	// triageInputCap keeps the untrusted path list within agent.MaxUntrusted.
	triageInputCap = 48
)

// step is one finished executor run of a worker job.
type step struct {
	h   executor.Handle
	st  executor.Status
	res agent.Result
}

// stepUsage is the usage one agent step reported, keyed by its executor job id.
type stepUsage struct {
	step string
	u    state.StepUsage
}

// job is the state of one claimed worker job while its watcher runs.
type job struct {
	l    *Loop
	env  controller.Env
	cfg  *config.Resolved
	role config.Role
	is   github.Issue
	log  *slog.Logger
	sj   state.Job

	head     string // the latest commit pushed (or the base when nothing was)
	branch   string
	exists   bool // the branch is on the remote
	pushed   bool // this job pushed to it
	deadline time.Time

	inputs    []executor.File
	untrusted []string

	final     step              // the latest agent step: close-out reads its worktree
	live      []executor.Handle // agent steps whose worktrees still exist
	agentLogs []string          // logs of the agent steps that completed
	cost      float64           // spent so far, for max_budget_usd
	usage     []stepUsage       // what each agent step reported, re-recorded at finish

	summary  string // redacted summary of the latest role file
	withheld bool
	fetched  []closeout.Fetched // validated fetched inputs of the latest role file; nil when withheld
	large    string             // the diff guard's latest "Large change" note; "" when under diff_warn_lines

	stepBudget float64 // max_budget_usd the current agent step runs with
}

// run claims one granted candidate and watches the job to the end, following
// the contract in package reaper. It runs in an Inflight goroutine and holds
// the pool slot until FinishWith has returned.
func (l *Loop) run(ctx context.Context, env controller.Env, cfg *config.Resolved, c candidate, slot *pool.Slot) {
	defer slot.Release()
	d := l.d
	n, role := c.issue.Number, c.role
	log := l.d.Log.With("issue", n, "role", role.Name)
	if env.Log != nil {
		log = env.Log.With("issue", n, "role", role.Name)
	}

	// 1. Read what the agent will see and find the branch. Nothing is claimed yet.
	comments, err := d.Reader.ListComments(ctx, n, time.Time{})
	if err != nil {
		log.Error("worker.claim_failed", "step", "list_comments", "error", err)
		return
	}
	branch, exists, err := l.resolveBranch(ctx, role.Name, n, c.issue.Title)
	if err != nil {
		if errors.Is(err, errAmbiguousBranch) {
			l.ambiguous(ctx, log, n)
			return
		}
		log.Error("worker.claim_failed", "step", "resolve_branch", "error", err)
		return
	}
	dir := agentjob.InputDir(n)
	inputs := []executor.File{
		{Path: dir + "/issue.md", Data: agentjob.Snapshot(c.issue)},
		{Path: dir + "/comments.md", Data: agentjob.Comments(comments)},
	}
	untrusted := []string{dir + "/issue.md", dir + "/comments.md"}
	if exists {
		f, ok, err := l.reviewFeedback(ctx, role, n, branch)
		if err != nil {
			log.Error("worker.claim_failed", "step", "review_feedback", "error", err)
			return
		}
		if ok {
			inputs = append(inputs, f)
			untrusted = append(untrusted, f.Path)
		}
	}

	// 2. The claim record.
	nj := state.NewJob{Stage: stageWorker, Role: role.Name, Issue: n}
	if exists {
		nj.Branch = branch
	}
	nj.Data, _ = json.Marshal(map[string]string{"branch": branch})
	sj, err := d.Store.CreateJob(ctx, nj)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			return
		}
		log.Error("worker.claim_failed", "step", "create_job", "error", err)
		return
	}
	slot.SetJobID(sj.ID)
	log = log.With("job", sj.ID)

	// 3. The running label. If it fails nothing has started, so no close-out.
	running := labels.Running(labels.StageWorker, role.Name)
	if err := d.Effects.AddLabels(ctx, n, running); err != nil {
		bg := context.WithoutCancel(ctx)
		if rerr := d.Effects.RemoveLabel(bg, n, running); rerr != nil {
			log.Warn("worker.unlabel_failed", "error", rerr)
		}
		log.Error("worker.claim_failed", "step", "add_running_label", "error", err)
		if ferr := d.Store.Fail(bg, sj.ID, "claim: add running label failed", d.Now()); ferr != nil {
			log.Error("worker.fail_record_failed", "error", ferr)
		}
		return
	}
	log.Info("worker.claim", "branch", branch, "branch_exists", exists)

	jb := &job{l: l, env: env, cfg: cfg, role: role, is: c.issue, log: log, sj: sj,
		branch: branch, exists: exists, inputs: inputs, untrusted: untrusted}
	jb.exec(ctx)
}

// ambiguous reports more than one branch for one role and issue (only a human
// can have made the second) and parks the issue. No job is claimed.
func (l *Loop) ambiguous(ctx context.Context, log *slog.Logger, n int) {
	marker := "<!-- " + labels.Prefix + "worker branches -->"
	text := fmt.Sprintf("**ard · worker · not started**\n\nThere is more than one branch for this issue and worker role, so ard cannot tell which one to continue. "+
		"Delete the extra branch and remove `%s` to continue.", labels.NeedsHuman)
	if err := agentjob.PostOnce(ctx, l.d.Reader, l.d.Effects, n, marker, text); err != nil {
		log.Error("worker.ambiguous_failed", "error", err)
		return
	}
	if err := l.d.Effects.AddLabels(ctx, n, labels.NeedsHuman); err != nil {
		log.Error("worker.ambiguous_failed", "error", err)
		return
	}
	log.Warn("worker.ambiguous_branches")
}

func (jb *job) budgetCap() float64 {
	if jb.role.MaxBudgetUSD > 0 {
		return jb.role.MaxBudgetUSD
	}
	return jb.cfg.MaxBudgetUSD
}

func (jb *job) cause(kind closeout.CauseKind, detail string, retry bool) closeout.Cause {
	return reaper.NewCause(jb.sj, kind, detail, retry)
}

func (jb *job) remaining() time.Duration { return jb.deadline.Sub(jb.l.d.Now()) }

// exec runs the job: the agent step, the guard and push, the tests with their
// fix steps, the PR, then close-out.
func (jb *job) exec(ctx context.Context) {
	d := jb.l.d
	if _, err := d.Store.StartAttempt(ctx, jb.sj.ID); err != nil {
		jb.log.Error("worker.start_failed", "step", "start_attempt", "error", err)
		jb.finish(ctx, step{}, nil, jb.cause(closeout.CauseNoProcess, "could not start the job", false))
		return
	}
	timeout := jb.role.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	jb.deadline = d.Now().Add(timeout)
	ref := jb.cfg.SHA
	if jb.exists {
		ref = localRef(jb.branch)
	}
	cur, ok := jb.agentStep(ctx, jb.sj.ID, ref, nil)
	if !ok {
		return
	}
	// An existing branch with no new code goes straight to the tests.
	head, _, done := jb.round(ctx, cur)
	if done {
		return
	}
	res := closeout.WorkerResult{Tests: closeout.TestsNone}
	if jb.role.Tests != "" {
		var done bool
		if res, done = jb.testLoop(ctx, head); done {
			return
		}
	}
	if !jb.openPR(ctx, &res) {
		return
	}
	jb.finish(ctx, jb.final, &res, closeout.Cause{})
}

// agentStep runs an agent (or fix) step on ref. ok is false when the job has
// been finished or stopped.
func (jb *job) agentStep(ctx context.Context, id, ref string, extra []executor.File) (step, bool) {
	n := jb.is.Number
	ej := jb.execJob(id, ref, jb.stepTimeout())
	if !jb.role.Network {
		ej.Network = executor.NetworkAdvisory
	}
	ej.Inputs = append(append([]executor.File(nil), jb.inputs...), extra...)
	ej.Scrub = []string{agent.RoleFilePath(n, stageWorker, jb.role.Name)}
	untrusted := append([]string(nil), jb.untrusted...)
	for _, f := range extra {
		untrusted = append(untrusted, f.Path)
	}
	// B: max_budget_usd caps the whole job. Each step gets what is left.
	if budget := jb.budgetCap(); budget > 0 {
		left := budget - jb.cost
		if left <= 0 {
			jb.log.Warn("worker.budget_exhausted", "cap_usd", budget, "spent_usd", jb.cost)
			jb.finishWithPR(ctx, jb.final, "", jb.cause(closeout.CauseNoProcess, "job budget exhausted (max_budget_usd)", false))
			return step{}, false
		}
		jb.stepBudget = left
	} else {
		jb.stepBudget = 0
	}
	s, cause, stopped := jb.runStep(ctx, ej, func(h executor.Handle) (executor.Command, error) {
		// A: plan.md is passed only when triage wrote it.
		untrusted = append(untrusted, jb.triageInputs(h.Worktree)...)
		return jb.agentCommand(h, untrusted)
	}, true, true)
	if stopped {
		return step{}, false
	}
	if cause != nil {
		jb.finishWithPR(ctx, s, "", *cause)
		return step{}, false
	}
	// s is an agent or fix step here, never a test step.
	if jb.l.d.Readings != nil && len(s.res.Windows) > 0 && s.h.JobID != "" {
		jb.l.d.Readings.Observe(ctx, s.res.Windows)
	}
	if u, ok := reaper.StepUsageOf(s.res); ok && s.h.JobID != "" {
		jb.usage = append(jb.usage, stepUsage{s.h.JobID, u})
		jb.recordUsage(ctx, jb.usage[len(jb.usage)-1])
	}
	if s.res.CostUSD != nil {
		jb.cost += *s.res.CostUSD
	} else {
		jb.log.Warn("worker.no_cost_reported", "step", id)
	}
	jb.log.Info("worker.step", "id", id, "kind", "agent", "exit", s.st.ExitCode, "state", string(s.st.State))
	if jb.abnormal(ctx, s.st) {
		jb.finishWithPR(ctx, s, "", closeout.Cause{})
		return step{}, false
	}
	if s.st.ExitCode != 0 {
		jb.finishWithPR(ctx, s, "", closeout.Cause{})
		return step{}, false
	}
	// This step supersedes the earlier agent worktrees.
	for _, h := range jb.live {
		if h.JobID != s.h.JobID {
			jb.cleanup(ctx, h)
		}
	}
	jb.live = []executor.Handle{s.h}
	if s.h.LogPath != "" {
		jb.agentLogs = append(jb.agentLogs, s.h.LogPath)
	}
	jb.final = s
	return s, true
}

// stepTimeout is the budget a step starts with: what is left of the job's time.
func (jb *job) stepTimeout() time.Duration {
	if r := jb.remaining(); r > 0 {
		return r
	}
	return time.Second
}

func (jb *job) execJob(id, ref string, timeout time.Duration) executor.Job {
	cfg, role := jb.cfg, jb.role
	ej := executor.Job{
		ID: id, Repo: jb.l.d.Repo.String(), Issue: jb.is.Number, Stage: stageWorker, Role: role.Name,
		RepoDir: jb.l.d.RepoDir, Ref: ref, RoleDir: filepath.Join(cfg.Repo, filepath.FromSlash(role.Dir)),
		RoleSource: executor.RoleSource{SHA: cfg.SHA, Path: role.Dir},
		Timeout:    timeout,
	}
	if role.Dockerfile != "" {
		ej.Dockerfile = filepath.Join(cfg.Repo, filepath.FromSlash(role.Dockerfile))
	}
	if !role.Network {
		ej.Network = executor.NetworkAdvisory
	}
	return ej
}

// runStep prepares, starts and waits for one executor job. cause is set when
// the step could not run (s.h is the handle if one exists); stopped means the
// controller is shutting down and the job is left running.
func (jb *job) runStep(ctx context.Context, ej executor.Job, command func(executor.Handle) (executor.Command, error), parse, agentStep bool) (s step, cause *closeout.Cause, stopped bool) {
	d := jb.l.d
	fail := func(kind closeout.CauseKind, detail string, retry bool) (step, *closeout.Cause, bool) {
		c := jb.cause(kind, detail, retry)
		return s, &c, false
	}
	h, err := d.Executor.Prepare(ctx, ej)
	if err != nil {
		jb.log.Error("worker.prepare_failed", "step", ej.ID, "error", err)
		if errors.Is(err, executor.ErrClone) {
			return fail(closeout.CauseCloneFail, "could not clone the repository", !jb.pushed)
		}
		if errors.Is(err, executor.ErrInputsTooLarge) {
			return fail(closeout.CauseInputsTooLarge, executor.InputsTooLargeDetail, false)
		}
		return fail(closeout.CauseNoProcess, "could not prepare the job", false)
	}
	s.h = h
	if agentStep {
		jb.live = append(jb.live, h)
	}
	agentjob.SaveHandle(ctx, d.Store, jb.log, jb.sj.ID, h)
	cmd, err := command(h)
	if err != nil {
		jb.log.Error("worker.command_failed", "step", ej.ID, "error", err)
		return fail(closeout.CauseNoProcess, "could not start the agent", false)
	}
	if h, err = d.Executor.Start(ctx, h, cmd); err != nil {
		s.h = h
		jb.log.Error("worker.start_failed", "step", ej.ID, "error", err)
		return fail(closeout.CauseNoProcess, "could not start the agent", false)
	}
	s.h = h
	agentjob.SaveHandle(ctx, d.Store, jb.log, jb.sj.ID, h)
	st, err := agentjob.Wait(ctx, d.Executor, h, d.Poll, jb.env.Stopping)
	if err != nil {
		jb.log.Warn("worker.watch_ended", "step", ej.ID, "error", err)
		return s, nil, true
	}
	s.st = st
	if parse && d.Parse != nil {
		var perr error
		if s.res, perr = d.Parse(ctx, jb.cfg, jb.sj, h, st.ExitCode); perr != nil {
			jb.log.Warn("worker.parse_failed", "step", ej.ID, "error", perr)
		}
	}
	return s, nil, false
}

// abnormal reports a step that did not simply exit: the Reaper killed it, it
// timed out or its process vanished. The Finisher turns that into a cause.
func (jb *job) abnormal(ctx context.Context, st executor.Status) bool {
	if st.State != executor.Exited {
		return true
	}
	sj, err := jb.l.d.Store.Job(ctx, jb.sj.ID)
	return err == nil && sj.KillReason != ""
}

// abnormalCause names why a step that did not simply exit ended, from the
// re-read job row (a Reaper kill reason) and the step's status.
func (jb *job) abnormalCause(ctx context.Context, st executor.Status) closeout.Cause {
	sj, err := jb.l.d.Store.Job(ctx, jb.sj.ID)
	if err != nil {
		sj = jb.sj
	}
	c := reaper.CauseOf(sj, st, agent.Result{}, jb.sj.CreatedAt)
	if c.Kind == "" {
		c = jb.cause(closeout.CauseNoProcess, "the test step ended abnormally", false)
	}
	return c
}

func (jb *job) agentCommand(h executor.Handle, untrusted []string) (executor.Command, error) {
	d := jb.l.d
	return agentjob.AgentCommand(d.Agents, jb.cfg, jb.role, agent.StageWorker, jb.is.Number, jb.stepBudget, untrusted, h, d.Model)
}

// triageInputs lists the regular files triage left for the worker, as paths
// relative to the worktree: plan.md, the scanner's issue.md, the triage role
// files and the triage logs.
// They are agent-written, so the agent is told they are untrusted. Nothing is
// followed through a symlink.
func (jb *job) triageInputs(wt string) []string {
	n := jb.is.Number
	root, err := os.OpenRoot(wt)
	if err != nil {
		return nil
	}
	defer root.Close()
	regular := func(rel string) bool {
		fi, err := root.Lstat(rel)
		return err == nil && fi.Mode().IsRegular()
	}
	var out []string
	for _, rel := range []string{
		fmt.Sprintf(".ard/%d/plan.md", n),
		fmt.Sprintf(".ard/%d/issue.md", n),
	} {
		if regular(rel) {
			out = append(out, rel)
		}
	}
	list := func(dir, prefix string) {
		d, err := root.Open(dir)
		if err != nil {
			return
		}
		defer d.Close()
		names, err := d.Readdirnames(-1)
		if err != nil {
			return
		}
		sort.Strings(names)
		for _, name := range names {
			rel := dir + "/" + name
			if strings.HasPrefix(name, prefix) && regular(rel) && len(out) < triageInputCap {
				out = append(out, rel)
			}
		}
	}
	list(fmt.Sprintf(".ard/%d", n), "triage-")
	list(fmt.Sprintf(".ard/%d/logs", n), "triage-")
	list(fmt.Sprintf(".ard/%d", n), "review-")
	list(fmt.Sprintf(".ard/%d/logs", n), "review-")
	return out
}

// peekRole reads the role file the agent wrote, the way close-out will, to
// decide whether there is a result to act on. It never follows a symlink.
func (jb *job) peekRole(h executor.Handle) (closeout.RoleFile, bool) {
	rel := agent.RoleFilePath(jb.is.Number, stageWorker, jb.role.Name)
	root, err := os.OpenRoot(h.Worktree)
	if err != nil {
		return closeout.RoleFile{}, false
	}
	defer root.Close()
	fi, err := root.Lstat(rel)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > closeout.MaxRoleFileBytes {
		return closeout.RoleFile{}, false
	}
	f, err := root.Open(rel)
	if err != nil {
		return closeout.RoleFile{}, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, closeout.MaxRoleFileBytes+1))
	if err != nil {
		return closeout.RoleFile{}, false
	}
	rf, err := closeout.Parse(b)
	if err != nil || rf.Role != jb.role.Name {
		return closeout.RoleFile{}, false
	}
	return rf, true
}

// setSummary keeps the redacted summary of the role file for the commit and
// the PR. Known secrets and host paths are replaced first; a path-rule error,
// a scan hit or a scan error withholds it.
func (jb *job) setSummary(ctx context.Context, h executor.Handle, raw string) {
	d := jb.l.d
	sec, err := d.Secrets().WithPaths(closeout.PathRules(h.Worktree, h.Home, d.HostPaths)...)
	if err != nil {
		jb.log.Error("worker.summary_withheld", "cause", "path rules", "error", err)
		jb.summary, jb.withheld = "", true
		return
	}
	s := sec.String(raw)
	found, err := d.Scanner.Scan(ctx, strings.NewReader(s))
	if err != nil || len(found) > 0 {
		jb.summary, jb.withheld = "", true
		return
	}
	jb.summary, jb.withheld = s, false
}

// setFetched keeps the fetched inputs of the role file for the PR body. The
// rendered fields get the same redaction and scan as the summary; any change
// by redaction, a scan hit or an error drops the whole list.
func (jb *job) setFetched(ctx context.Context, h executor.Handle, fs []closeout.Fetched) {
	jb.fetched = nil
	if len(fs) == 0 {
		return
	}
	d := jb.l.d
	var text strings.Builder
	for _, f := range fs {
		fmt.Fprintf(&text, "%s %s %d %s %s %s\n", f.URL, f.Date, f.Status, f.SHA256, f.VerifiedBy, f.Path)
	}
	sec, err := d.Secrets().WithPaths(closeout.PathRules(h.Worktree, h.Home, d.HostPaths)...)
	if err != nil {
		jb.log.Error("worker.fetched_withheld", "cause", "path rules", "error", err)
		return
	}
	raw := text.String()
	if sec.String(raw) != raw {
		jb.log.Error("worker.fetched_withheld", "cause", "redacted")
		return
	}
	found, err := d.Scanner.Scan(ctx, strings.NewReader(raw))
	if err != nil || len(found) > 0 {
		jb.log.Error("worker.fetched_withheld", "cause", "scan")
		return
	}
	jb.fetched = fs
}

// round decides one finished agent step: the role file, the diff guard and the
// push. head is the commit the tests run on. done means the job has been
// finished (or stopped) here; noCode means the step changed nothing to commit.
func (jb *job) round(ctx context.Context, s step) (head string, noCode, done bool) {
	n := jb.is.Number
	rf, ok := jb.peekRole(s.h)
	if !ok || rf.Status != closeout.StatusDone {
		// Close-out reports a missing, malformed or not-done result itself.
		jb.finishWithPR(ctx, s, "", closeout.Cause{})
		return "", false, true
	}
	jb.setSummary(ctx, s.h, rf.Summary)
	jb.setFetched(ctx, s.h, rf.Fetched)
	stage, err := os.MkdirTemp("", "ard-code-")
	if err == nil {
		defer os.RemoveAll(stage)
		// The committer refuses a source path that involves a symlink (/var on macOS).
		stage, err = filepath.EvalSymlinks(stage)
	}
	if err != nil {
		jb.log.Error("worker.guard_failed", "error", err)
		jb.finishWithPR(ctx, s, "", jb.cause(closeout.CauseNoProcess, "the diff guard could not run", false))
		return "", false, true
	}
	changes, prLines, err := jb.l.facts(ctx, n, s.h.Worktree, s.h.BaseSHA, jb.cfg.SHA, stage)
	var le *gitx.LimitError
	if errors.As(err, &le) {
		// Too big or too many to look at: a hold, before anything was hashed.
		jb.log.Info("worker.guard", "hold", true, "limit", le.Error())
		jb.finishWithPR(ctx, s, "diff guard: "+Reject{Path: le.Path, Why: le.Why}.String(), closeout.Cause{})
		return "", false, true
	}
	if err != nil {
		jb.log.Error("worker.guard_failed", "error", err)
		jb.finishWithPR(ctx, s, "", jb.cause(closeout.CauseNoProcess, "the diff guard could not run", false))
		return "", false, true
	}
	lim := Lines{Warn: jb.role.DiffWarnLines, Max: jb.role.DiffMaxLines}
	v := Check(n, changes, prLines, jb.exists, lim)
	jb.log.Info("worker.guard", "hold", v.Hold != "", "lines", v.Lines, "rejects", len(v.Rejects), "files", len(v.Commit))
	if v.Hold != "" {
		jb.finishWithPR(ctx, s, v.Hold, closeout.Cause{})
		return "", false, true
	}
	// The latest round's line count decides the note: a fix round can shrink
	// the PR back under the warn threshold.
	jb.large = v.Large
	if v.Large != "" {
		jb.log.Info("worker.diff_large", "lines", v.Lines, "warn", lim.Warn, "max", lim.Max)
	}
	if v.NoCode {
		jb.head = s.h.BaseSHA
		return s.h.BaseSHA, true, false
	}
	sha, ok := jb.push(ctx, s, v)
	if !ok {
		return "", false, true
	}
	return sha, false, false
}

// push commits the guard's files to the branch through the code lane.
func (jb *job) push(ctx context.Context, s step, v Verdict) (string, bool) {
	d := jb.l.d
	req := committer.CodeRequest{
		Issue: jb.is.Number, Role: jb.role.Name, JobID: jb.sj.ID, Type: CommitType(jb.is.Labels),
		Title: jb.is.Title, Body: jb.summary, Base: s.h.BaseSHA, Create: !jb.exists,
	}
	for _, c := range v.Commit {
		cf := committer.CodeFile{Path: c.Path, Delete: c.Deleted}
		if !c.Deleted {
			if c.Copy == "" {
				jb.finishWithPR(ctx, s, "", jb.cause(closeout.CauseNoProcess, "could not push the branch: no scanned copy", false))
				return "", false
			}
			cf.Src, cf.Exec = c.Copy, c.Kind == Exec
		}
		req.Files = append(req.Files, cf)
	}
	res, err := d.Code.CommitCode(ctx, jb.branch, req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			jb.log.Warn("worker.push_stopped", "error", err)
			return "", false
		}
		kind := "push"
		var ge *committer.GuardError
		switch {
		case errors.Is(err, committer.ErrBranchExists):
			kind = "branch exists"
		case errors.Is(err, committer.ErrConflict):
			kind = "conflict"
		case errors.Is(err, committer.ErrBranchGone):
			kind = "branch gone"
		case errors.As(err, &ge):
			kind = "guard"
		}
		jb.log.Error("worker.push_failed", "kind", kind, "error", err)
		if errors.Is(err, committer.ErrTransient) {
			jb.finishWithPR(ctx, s, "", jb.cause(closeout.CausePushTransient, "could not push the branch: remote unavailable after retries", !jb.pushed))
			return "", false
		}
		jb.finishWithPR(ctx, s, "", jb.cause(closeout.CauseNoProcess, "could not push the branch: "+kind, false))
		return "", false
	}
	// The branch is on the remote from here: any failure below still opens the PR.
	jb.pushed = true
	if !jb.exists {
		jb.exists = true
		if err := d.Store.SetBranch(ctx, jb.sj.ID, jb.branch); err != nil {
			jb.log.Error("worker.set_branch_failed", "error", err)
			jb.finishWithPR(ctx, s, "", jb.cause(closeout.CauseNoProcess, "could not record the branch", false))
			return "", false
		}
	}
	jb.log.Info("worker.push", "branch", jb.branch, "sha", res.SHA, "files", len(req.Files), "noop", res.NoOp)
	jb.head = res.SHA
	return res.SHA, true
}

// testLoop runs the role's tests on head, with up to max_test_fixes fix steps.
// done means the job has been finished (or stopped) here.
func (jb *job) testLoop(ctx context.Context, head string) (res closeout.WorkerResult, done bool) {
	res.Tests = closeout.TestsFailed
	fixes := 0
	for k := 1; ; k++ {
		if jb.remaining() < minStepTime {
			res.Fixes, res.TestNote = fixes, testsRanOutOfTime
			return res, false
		}
		ej := jb.execJob(fmt.Sprintf("%st%d", jb.sj.ID, k), head, jb.remaining())
		if !jb.role.Network {
			// The test step carries no model credential, so Docker can enforce it.
			ej.Network = executor.NetworkNone
		}
		tests := jb.role.Tests
		ts, cause, stopped := jb.runStep(ctx, ej, func(executor.Handle) (executor.Command, error) {
			return executor.Command{Path: "/bin/sh", Args: []string{"-c", tests}}, nil
		}, false, false)
		if stopped {
			return res, true
		}
		if cause != nil {
			if ts.h.JobID != "" {
				jb.cleanup(ctx, ts.h)
			}
			jb.finishWithPR(ctx, jb.final, "", *cause)
			return res, true
		}
		if jb.abnormal(ctx, ts.st) {
			// A test step has no scrubbed role file and its log is not evidence
			// worth committing: close out through the agent step that made the
			// code, with the cause the test step ended for.
			jb.cleanup(ctx, ts.h)
			jb.finishWithPR(ctx, jb.final, "", jb.abnormalCause(ctx, ts.st))
			return res, true
		}
		jb.log.Info("worker.step", "id", ej.ID, "kind", "test", "exit", ts.st.ExitCode)
		if ts.st.ExitCode == 0 {
			jb.cleanup(ctx, ts.h)
			res.Tests, res.Fixes = closeout.TestsPassed, fixes
			return res, false
		}
		out := jb.testOutput(ts.h, tests, ts.st.ExitCode)
		jb.cleanup(ctx, ts.h)
		if fixes >= jb.role.MaxTestFixes {
			res.Fixes = fixes
			return res, false
		}
		if jb.remaining() < minStepTime {
			res.Fixes, res.TestNote = fixes, testsRanOutOfTime
			return res, false
		}
		fixes++
		fs, ok := jb.agentStep(ctx, fmt.Sprintf("%sf%d", jb.sj.ID, fixes), head, []executor.File{{
			Path: agentjob.InputDir(jb.is.Number) + "/test-output.log", Data: out}})
		if !ok {
			return res, true
		}
		nh, noCode, fdone := jb.round(ctx, fs)
		if fdone {
			return res, true
		}
		if noCode {
			res.Fixes, res.TestNote = fixes, "the fix changed no files"
			return res, false
		}
		head = nh
	}
}

// testOutput builds test-output.log: the untrusted header and the last 64 KiB
// of the test log, cut on a line and redacted.
func (jb *job) testOutput(h executor.Handle, command string, exit int) []byte {
	var tail []byte
	if f, err := os.Open(h.LogPath); err == nil {
		defer f.Close()
		if fi, err := f.Stat(); err == nil {
			off := max(0, fi.Size()-testOutputBytes)
			if _, err := f.Seek(off, io.SeekStart); err == nil {
				tail, _ = io.ReadAll(io.LimitReader(f, testOutputBytes))
				if off > 0 {
					if i := strings.IndexByte(string(tail), '\n'); i >= 0 {
						tail = tail[i+1:]
					}
				}
			}
		}
	}
	tail, _ = jb.l.d.Secrets().Bytes(tail)
	return []byte(fmt.Sprintf("%s\n## Test run\n\nCommand: %s\nExit code: %d\n\n## Test output (the end of the log)\n\n%s\n",
		agentjob.UntrustedHeader, oneLine(command), exit, tail))
}

// reviewers lists the reviewer roles to request on the PR. The base set is
// config's SelectReviewers: the role's reviewers (or every reviewer role)
// whose when: rule matches the PR's changed paths (merge base to head) or the
// triage roles that ran (closed done, or labelled done). Reviewers triage
// deferred are then added unconditionally; the worker's own next requests are
// added by close-out. Selection is monotonic: every run recomputes from the
// whole PR diff and close-out only adds labels, so earlier requests stay and
// newly matching reviewers are added. When the head is unknown or git fails,
// path rules fail open. Only roles that exist, without duplicates.
func (jb *job) reviewers(ctx context.Context) []string {
	changed := jb.changedPaths(ctx)
	ran := map[string]bool{}
	tr, err := closeout.TriageRoles(ctx, jb.l.d.Store, jb.is.Number)
	if err != nil {
		jb.log.Warn("worker.reviewers_failed", "step", "triage", "error", err)
	}
	for k := range tr {
		ran[k] = true
	}
	for _, r := range jb.cfg.RolesOf(config.KindTriage) {
		for _, l := range jb.is.Labels {
			if l == labels.Done(labels.StageTriage, r.Name) {
				ran[r.Name] = true
			}
		}
	}
	names := jb.cfg.SelectReviewers(jb.role, changed, func(r string) bool { return ran[r] })
	deferred, err := closeout.Deferred(ctx, jb.l.d.Store, jb.is.Number)
	if err != nil {
		jb.log.Warn("worker.deferred_failed", "error", err)
	}
	for _, r := range deferred {
		if r.Stage == closeout.StageReview {
			names = append(names, r.Role)
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, nm := range names {
		if _, ok := jb.cfg.Role(config.KindReviewer, nm); ok && !seen[nm] {
			seen[nm] = true
			out = append(out, nm)
		}
	}
	if len(out) == 0 && len(jb.cfg.RolesOf(config.KindReviewer)) > 0 {
		jb.log.Warn("worker.no_reviewers", "issue", jb.is.Number, "branch", jb.branch)
	}
	return out
}

// changedPaths lists the paths the PR changes (merge base of the config
// commit and head, to head) without the .ard/ and .ard-input/ trees. A nil
// result means the paths are unknown: path rules then fail open.
func (jb *job) changedPaths(ctx context.Context) []string {
	if jb.head == "" || strings.Trim(jb.head, "0") == "" {
		return nil
	}
	g, dir := jb.l.d.Git, jb.l.d.RepoDir
	base, err := g.MergeBase(ctx, dir, jb.cfg.SHA, jb.head)
	step := "merge_base"
	var ps []string
	if err == nil {
		step = "changed_paths"
		ps, err = g.ChangedPaths(ctx, dir, base, jb.head)
	}
	if err != nil {
		jb.log.Warn("worker.reviewers_failed", "step", step, "error", err)
		return nil
	}
	out := []string{}
	for _, p := range ps {
		if p == ".ard" || p == ".ard-input" || strings.HasPrefix(p, ".ard/") || strings.HasPrefix(p, ".ard-input/") {
			continue
		}
		out = append(out, p)
	}
	return out
}

// openPR creates or updates the draft PR and records it. The PR stays a draft:
// the reviewer loop marks it ready once every requested review passes. It reports false after finishing the job.
func (jb *job) openPR(ctx context.Context, res *closeout.WorkerResult) bool {
	d := jb.l.d
	n := jb.is.Number
	res.Reviewers = jb.reviewers(ctx)
	res.Large = jb.large
	res.Fetched = jb.fetched
	title, err := Title(CommitType(jb.is.Labels), jb.is.Title, n, jb.sj.ID)
	if err != nil {
		jb.log.Error("worker.pr_failed", "step", "title", "error", err)
		jb.finish(ctx, jb.final, nil, jb.cause(closeout.CauseNoProcess, "could not open the pull request", false))
		return false
	}
	body := Body(BodyInfo{Repo: d.Repo, Issue: n, Role: jb.role.Name, Branch: jb.branch, JobID: jb.sj.ID,
		Summary: jb.summary, Withheld: jb.withheld, Result: *res})
	fail := func(step string, err error) bool {
		jb.log.Error("worker.pr_failed", "step", step, "error", err)
		jb.finish(ctx, jb.final, nil, jb.cause(closeout.CauseNoProcess, "could not open the pull request", false))
		return false
	}
	prs, err := d.Reader.ListPRs(ctx, github.ListPRsOpts{State: github.StateOpen, Head: jb.branch})
	if err != nil {
		return fail("list", err)
	}
	var pr github.PullRequest
	if len(prs) > 0 {
		pr = prs[0]
		if err := d.Effects.UpdatePR(ctx, pr.Number, github.PRUpdate{Title: &title, Body: &body}); err != nil {
			return fail("update", err)
		}
	} else if pr, err = d.Effects.CreatePR(ctx, github.NewPR{Title: title, Body: body, Head: jb.branch, Base: "main", Draft: true}); err != nil {
		return fail("create", err)
	}
	if err := d.Store.SetPR(ctx, jb.sj.ID, pr.Number); err != nil {
		return fail("record", err)
	}
	res.PR, res.PRURL = pr.Number, pr.URL
	jb.log.Info("worker.pr", "pr", pr.Number, "tests", string(res.Tests), "reviewers", len(res.Reviewers))
	return true
}

func (jb *job) cleanup(ctx context.Context, h executor.Handle) {
	if h.JobID == "" {
		return
	}
	if err := jb.l.d.Executor.Cleanup(context.WithoutCancel(ctx), h, executor.CleanupOptions{}); err != nil {
		jb.log.Warn("worker.cleanup_failed", "step", h.JobID, "error", err)
	}
}

// finish ends the job through the Finisher with the handle of the step that
// close-out should read. It first cleans every other worktree this job made.
func (jb *job) finish(ctx context.Context, s step, w *closeout.WorkerResult, cause closeout.Cause) {
	d := jb.l.d
	for _, h := range jb.live {
		if h.JobID != s.h.JobID {
			jb.cleanup(ctx, h)
		}
	}
	jb.live = nil
	var extra []string
	for _, p := range jb.agentLogs {
		if p != s.h.LogPath {
			extra = append(extra, p)
		}
	}
	// Re-record every agent step: idempotent, and it repairs a step whose
	// earlier write failed. The Finisher then reads the job's totals from state.
	for _, su := range jb.usage {
		jb.recordUsage(ctx, su)
	}
	if w != nil {
		w.Pushed = jb.pushed
	}
	res := s.res
	// agentStep already recorded this step's readings; recording them again
	// here could overwrite a newer reading with this older one.
	res.Windows = nil
	if s.h.JobID != "" {
		agentjob.SaveHandle(ctx, d.Store, jb.log, jb.sj.ID, s.h)
	}
	out, err := d.Finisher.FinishWith(ctx, jb.sj.ID, s.h, s.st, res, reaper.FinishOptions{Cause: cause, Worker: w, ExtraLogs: extra})
	if err != nil {
		// ErrBusy or any other error leaves a resumable close-out for the Reaper.
		jb.log.Error("worker.closeout_failed", "error", err)
		return
	}
	jb.log.Info("worker.closeout", "outcome", string(out))
}

// recordUsage stores one step's usage. A failure is logged: accounting never
// stops a job.
func (jb *job) recordUsage(ctx context.Context, su stepUsage) {
	if _, err := jb.l.d.Store.RecordStepUsage(ctx, jb.sj.ID, su.step, su.u); err != nil {
		jb.log.Warn("worker.usage_record_failed", "step", su.step, "error", err)
	}
}

// oneLine keeps a configured command on one line for the log header.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// finishWithPR ends the job like finish, but first opens the draft PR when
// this job has already pushed the branch, so pushed work is never left without
// one. hold, when set, turns the result into needs-human with that reason. The
// tests are reported as failed: they did not finish.
func (jb *job) finishWithPR(ctx context.Context, s step, hold string, cause closeout.Cause) {
	if !jb.pushed {
		var w *closeout.WorkerResult
		if hold != "" {
			w = &closeout.WorkerResult{Hold: hold}
		}
		jb.finish(ctx, s, w, cause)
		return
	}
	res := closeout.WorkerResult{Tests: closeout.TestsFailed, TestNote: "stopped before the tests finished", Hold: hold}
	if !jb.openPR(ctx, &res) {
		return
	}
	jb.finish(ctx, s, &res, cause)
}
