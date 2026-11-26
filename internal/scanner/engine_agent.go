package scanner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"regexp"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/limits"
	"github.com/albertcbrown/ard/internal/modelauth"
	"github.com/albertcbrown/ard/internal/reaper"
	"github.com/albertcbrown/ard/internal/state"
)

// UsageRecorder is the part of *state.Store the agent engine uses.
type UsageRecorder interface {
	RecordStepUsage(ctx context.Context, jobID, step string, u state.StepUsage) (bool, error)
}

// LimitRecorder is the part of *limits.Book the agent engine uses.
type LimitRecorder interface {
	Exhaust(ctx context.Context, family string, resetsAt time.Time) (time.Time, error)
	Succeeded(ctx context.Context, family string) error
}

// AgentDeps are the collaborators of AgentEngine.
type AgentDeps struct {
	ExecDeps
	Agents *agent.Registry
	Cred   modelauth.Credential // zero: Ready and Scan return ErrNoCredential
	Usage  UsageRecorder        // nil: usage is not recorded
	Limits func() LimitRecorder // lazy like Pool; nil, or a nil result, means a limit cannot be recorded
}

var (
	// ErrNoCredential means no model credential is set, so an LLM scanner
	// cannot run. The run is not recorded: the scanner stays due.
	ErrNoCredential = errors.New("scanner: no model credential")
	// ErrUsageLimit means the run stopped at a usage limit, now recorded in
	// the limits book. The run is not recorded; the pool blocks the model
	// family until the reset.
	ErrUsageLimit = errors.New("scanner: usage limit")
)

// subtypeRE is the shape of a result subtype that may appear in an error.
var subtypeRE = regexp.MustCompile(`^[a-z_]{1,40}$`)

// AgentEngine runs a prompt.txt scanner as an agent job: a read-only worktree
// at cfg.SHA, the role's sidecar, a writable output dir and one model
// credential. The agent writes its findings as JSON lines to
// $ARD_OUT/findings.jsonl. Its errors are fixed text: nothing the agent wrote
// reaches a log, an error or the job record.
type AgentEngine struct{ d AgentDeps }

var _ Engine = (*AgentEngine)(nil)

// NewAgentEngine applies defaults to d.
func NewAgentEngine(d AgentDeps) *AgentEngine {
	d.ExecDeps = d.ExecDeps.withDefaults()
	return &AgentEngine{d: d}
}

// Ready reports whether the engine can run role, before any GitHub call or
// slot: ErrNoCredential when no model credential is set.
func (x *AgentEngine) Ready(role config.Role) error {
	switch {
	case x.d.Cred.IsZero():
		return ErrNoCredential
	case x.d.Agents == nil:
		return errors.New("scanner: no agent registry")
	case !role.LLMScanner():
		return fmt.Errorf("scanner %s: not a prompt.txt scanner", role.Name)
	}
	return nil
}

// Scan runs role's agent on cfg.SHA in a pool slot (ErrNoSlot when denied)
// and returns the findings it wrote. Usage is recorded before any outcome
// check, so a failed run keeps its cost. A usage-limit stop is ErrUsageLimit
// once the limits book holds it; a timeout, a vanished process, a bad exit, an
// unsuccessful result or a missing or invalid findings file is a *ScanError.
func (x *AgentEngine) Scan(ctx context.Context, cfg *config.Resolved, role config.Role, since time.Time) (ScanResult, error) {
	d := x.d
	log := d.Log.With("scanner", role.Name)
	if cfg == nil || cfg.SHA == "" {
		return ScanResult{}, fmt.Errorf("scan %s: config has no commit to pin the scan to", role.Name)
	}
	if err := x.Ready(role); err != nil {
		return ScanResult{}, err
	}
	sha := cfg.SHA
	return runJob(ctx, d.ExecDeps, log, scanJob{
		role: role, sha: sha, since: since,
		job: func(id string) executor.Job {
			// The whole repository and the sidecar are untrusted input.
			return agentjob.ExecJob(cfg, role, executor.Job{
				ID: id, Repo: d.Repo, Stage: "scanner", Role: role.Name,
				RepoDir: d.RepoDir, Ref: sha, ReadOnly: true, Output: true, TrustedInput: false,
			})
		},
		build: func(h executor.Handle) (executor.Command, error) {
			if h.OutDir == "" {
				return executor.Command{}, errors.New("the executor gave the job no output dir")
			}
			return agentjob.ScannerCommand(d.Agents, cfg, role, h, d.Cred)
		},
		finish: func(ctx context.Context, job state.Job, h executor.Handle, st executor.Status) (ScanResult, error) {
			res := x.parse(ctx, log, role, h, st)
			x.recordUsage(ctx, log, job.ID, res)
			if err := x.limitStop(ctx, log, job.ID, res); err != nil {
				return ScanResult{}, err
			}
			if se := runFailure(sha, st, res); se != nil {
				return ScanResult{}, se
			}
			if l := x.limits(); l != nil {
				if err := l.Succeeded(context.WithoutCancel(ctx), limits.FamilyOf(res.Model)); err != nil {
					log.Warn("scanner.limits_succeeded", "job", job.ID, "error", err)
				}
			}
			fs, err := readFindings(h.OutDir)
			if err != nil {
				return ScanResult{}, &ScanError{SHA: sha, Err: err}
			}
			if errs := CheckLLMFingerprints(fs, existsIn(h.Worktree)); len(errs) > 0 {
				// The reasons are fixed text; the findings are not logged.
				for _, e := range errs {
					log.Warn("scanner.llm_grammar", "job", job.ID, "line", e.Line, "reason", e.Msg)
				}
				return ScanResult{}, &ScanError{SHA: sha, Err: errors.New(LLMGrammarLines(errs))}
			}
			return ScanResult{SHA: sha, Findings: fs}, nil
		},
	})
}

func (x *AgentEngine) limits() LimitRecorder {
	if x.d.Limits == nil {
		return nil
	}
	return x.d.Limits()
}

// parse reads the run's result from its stdout. A run with no result event
// (killed or timed out) keeps the partial result; any other failure leaves
// the zero result.
func (x *AgentEngine) parse(ctx context.Context, log *slog.Logger, role config.Role, h executor.Handle, st executor.Status) agent.Result {
	a, err := x.d.Agents.Get(role.Agent)
	if err != nil {
		log.Warn("scanner.agent_parse_failed", "job", h.JobID, "error", err)
		return agent.Result{}
	}
	rc, err := x.d.Executor.Logs(ctx, h, executor.LogsOptions{Stream: executor.Stdout})
	if err != nil {
		log.Warn("scanner.agent_parse_failed", "job", h.JobID, "error", err)
		return agent.Result{}
	}
	defer rc.Close()
	res, err := a.Parse(rc, st.ExitCode)
	if err != nil && !errors.Is(err, agent.ErrNoResult) {
		log.Warn("scanner.agent_parse_failed", "job", h.JobID, "error", "unparsable agent output")
		return agent.Result{}
	}
	return res
}

// recordUsage records the run's usage under the job's own step. A store error
// is logged, never returned: accounting does not decide the run.
func (x *AgentEngine) recordUsage(ctx context.Context, log *slog.Logger, jobID string, res agent.Result) {
	u, ok := reaper.StepUsageOf(res)
	if !ok {
		return
	}
	attrs := []any{"job", jobID, "models", u.Models, "subtype", safeSubtype(res.Subtype), "ok", res.OK}
	if u.CostUSD != nil {
		attrs = append(attrs, "cost_usd", *u.CostUSD)
	}
	if t := u.Tokens; t != nil {
		attrs = append(attrs, "tokens_in", t.Input, "tokens_out", t.Output, "tokens_cache_write", t.CacheWrite, "tokens_cache_read", t.CacheRead)
	}
	log.Info("scanner.agent_usage", attrs...)
	if x.d.Usage == nil {
		return
	}
	if _, err := x.d.Usage.RecordStepUsage(context.WithoutCancel(ctx), jobID, jobID, u); err != nil {
		log.Warn("scanner.usage_record_failed", "job", jobID, "error", err)
	}
}

// limitStop records a usage-limit stop in the limits book and returns a
// *limitStop wrapping ErrUsageLimit, or nil when res is no limit stop. When
// the book cannot record it, the error is a plain one, so the run is recorded
// and the scanner does not re-run into the limit at once (fail closed).
func (x *AgentEngine) limitStop(ctx context.Context, log *slog.Logger, jobID string, res agent.Result) error {
	cause, ok := reaper.UsageLimitClassifier(x.d.Cred.Auth)(res)
	if !ok {
		return nil
	}
	l := x.limits()
	if l == nil {
		return errors.New("usage limit not recorded: no limits book")
	}
	until, err := l.Exhaust(context.WithoutCancel(ctx), cause.Family, cause.ResetsAt)
	if err != nil {
		log.Warn("scanner.limit_record_failed", "job", jobID, "error", err)
		return errors.New("usage limit not recorded")
	}
	log.Warn("scanner.usage_limit", "job", jobID, "family", cause.Family, "until", until.UTC())
	return &limitStop{err: fmt.Errorf("%w: %s", ErrUsageLimit, cause.Detail)}
}

// runFailure is the *ScanError of a run that did not succeed, or nil. Its
// text holds the exit code and a well-formed subtype only.
func runFailure(sha string, st executor.Status, res agent.Result) *ScanError {
	switch {
	case st.State == executor.TimedOut:
		return &ScanError{SHA: sha, TimedOut: true, Exit: st.ExitCode}
	case st.State == executor.Gone:
		return &ScanError{SHA: sha, Exit: -1, Err: errors.New("agent process vanished")}
	case st.ExitCode != 0 || !res.OK:
		return &ScanError{SHA: sha, Exit: st.ExitCode, Err: fmt.Errorf("agent run failed: exit %d, subtype %s", st.ExitCode, safeSubtype(res.Subtype))}
	}
	return nil
}

func safeSubtype(s string) string {
	if subtypeRE.MatchString(s) {
		return s
	}
	return "unknown"
}

// readFindings reads and decodes dir/findings.jsonl: a regular file (never a
// symlink) of at most MaxStreamBytes. Its errors are fixed text with line
// numbers only, never what the agent wrote.
func readFindings(dir string) ([]Finding, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.New("cannot open the output dir")
	}
	defer root.Close()
	fi, err := root.Lstat(agentjob.ScannerFindingsFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, errors.New("no findings file")
	case err != nil:
		return nil, errors.New("cannot stat the findings file")
	case !fi.Mode().IsRegular():
		return nil, errors.New("findings file is not a regular file")
	}
	f, err := root.Open(agentjob.ScannerFindingsFile)
	if err != nil {
		return nil, errors.New("cannot open the findings file")
	}
	defer f.Close()
	// Stat the open file too: the file checked above may have been swapped.
	fi, err = f.Stat()
	switch {
	case err != nil:
		return nil, errors.New("cannot stat the findings file")
	case !fi.Mode().IsRegular():
		return nil, errors.New("findings file is not a regular file")
	case fi.Size() > MaxStreamBytes:
		return nil, fmt.Errorf("findings file is larger than %d bytes", MaxStreamBytes)
	}
	out, err := DecodeFindings(io.LimitReader(f, MaxStreamBytes+1))
	var de DecodeErrors
	switch {
	case errors.As(err, &de):
		// DecodeErrors quote values; only their line numbers are kept.
		return nil, errors.New(LLMGrammarLines(de))
	case err != nil:
		return nil, errors.New("cannot read the findings file")
	}
	return out, nil
}

// existsIn reports whether a repo-relative path exists in the worktree,
// without following a symlink out of it.
func existsIn(worktree string) func(string) bool {
	return func(p string) bool {
		root, err := os.OpenRoot(worktree)
		if err != nil {
			return false
		}
		defer root.Close()
		_, err = root.Lstat(p)
		return err == nil
	}
}
