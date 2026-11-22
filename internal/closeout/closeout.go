package closeout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/state"
)

// recordVersion stays 1 across the Reaper's additions (Job.Cause, Plan.Tail,
// Plan.TailWithheld, Plan.KeepUntil): older records decode them as zero, and
// zero means the earlier behaviour.
const recordVersion = 1

// failedKeep is how long a failed job's resources are kept (proposal: 24 h).
const failedKeep = 24 * time.Hour

// Job is everything close-out needs, built by the caller from the state job
// and the executor handle. It is persisted inside the Record, so Resume needs
// nothing else.
type Job struct {
	ID       string // ULID
	Issue    int    // negative only in dry-run
	PR       int    // 0 until a PR exists; required for a review
	Stage    string // triage | worker | review
	Role     string
	Attempt  int
	Branch   string // "" commits to main (triage); worker/<name> otherwise
	Worktree string // executor handle Worktree
	BaseSHA  string // executor handle BaseSHA: what the worktree started from
	LogPath  string // executor handle LogPath; "" when there is no log
	// Scrubbed is copied from the executor handle: the paths it removed from
	// the worktree before the agent ran. An unchanged role file is trusted
	// only if listed here.
	Scrubbed []string
	ExitCode int
	CostUSD  float64 // 0 when unknown
	// Cause, when Kind is set, says why the job ended without a usable result.
	// Close-out then skips the role file and the changed files and commits only
	// the log. The zero Cause decides from the role file and exit code.
	Cause Cause
}

// CauseKind names why a job ended without a usable result.
type CauseKind string

// Cause kinds. The reason in the comment is "<kind>: <detail>".
const (
	CauseQuiet     CauseKind = "heartbeat_quiet"    // no log output for 10 minutes
	CauseDeadline  CauseKind = "deadline"           // ran past its timeout
	CauseRestart   CauseKind = "controller_restart" // orphaned, gone or never started across a restart
	CauseNoProcess CauseKind = "no_process"         // never started, or its process vanished in this run
	CauseRateLimit CauseKind = "rate_limit"         // the agent reported a transient API error
	CauseCloneFail CauseKind = "clone_failed"       // a loop's Prepare failed at git or clone
)

// Cause is why a job ended. Detail is fixed, non-secret text.
type Cause struct {
	Kind      CauseKind
	Detail    string
	Transient bool // a named transient cause
	Retry     bool // hand off to one automatic retry: Transient and the job is not itself a retry
}

// ErrBusy means another close-out of the same job is in flight. Run and
// ResumeJob wrap it; callers skip quietly and look again later.
var ErrBusy = errors.New("closeout: another close-out of this job is in flight")

// Phase is how far close-out got.
type Phase string

const (
	PhasePrepared  Phase = "prepared"
	PhaseCommitted Phase = "committed"
	PhaseRequested Phase = "requested"
	PhaseCommented Phase = "commented"
	PhaseClosed    Phase = "closed"
)

// Record is the persisted state of one close-out.
type Record struct {
	Version   int
	Phase     Phase
	Job       Job
	Plan      Plan
	SHA       string
	Synthetic bool // the commit was a dry-run: nothing reached the remote
	CommentID int64
}

// Effects is the subset of effects.Effects close-out uses.
type Effects interface {
	AddLabels(ctx context.Context, n int, labels ...string) error
	RemoveLabel(ctx context.Context, n int, label string) error
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
	DryRun() bool
}

// Comments lists existing comments (github.Reader).
type Comments interface {
	ListComments(ctx context.Context, n int, since time.Time) ([]github.Comment, error)
}

// Committer commits a file set to main (branch "") or a PR branch.
type Committer interface {
	Commit(ctx context.Context, branch string, req committer.Request) (committer.Result, error)
}

// Redactor is *redact.Redactor.
type Redactor interface {
	Log(ctx context.Context, src, dst string) (redact.LogResult, error)
	Text(ctx context.Context, b []byte) (redact.TextResult, error)
}

// Store is the part of state.Store close-out uses.
type Store interface {
	FailRetry(ctx context.Context, id, reason string, keepUntil time.Time) error
	Job(ctx context.Context, id string) (state.Job, error)
	SetCloseout(ctx context.Context, id, phase string, plan json.RawMessage) error
	OpenCloseouts(ctx context.Context) ([]state.Job, error)
	Complete(ctx context.Context, id string, costUSD *float64) error
	Fail(ctx context.Context, id, reason string, keepUntil time.Time) error
}

// Deferred returns the review requests that triage jobs of an issue recorded
// but could not apply (no PR existed yet), de-duplicated, in request order. The
// Phase 1 worker loop calls it when it opens the PR and adds the
// ard:review:<role> labels to the PR itself. Only closed, done triage
// close-outs count.
func Deferred(ctx context.Context, s interface {
	CloseoutsOf(ctx context.Context, issue int, stage string) ([]state.Job, error)
}, issue int) ([]NextReq, error) {
	jobs, err := s.CloseoutsOf(ctx, issue, StageTriage)
	if err != nil {
		return nil, fmt.Errorf("deferred reviewers of issue %d: %w", issue, err)
	}
	seen := map[NextReq]bool{}
	var out []NextReq
	for _, sj := range jobs {
		rec, err := decode(sj)
		if err != nil {
			return nil, err
		}
		if rec.Phase != PhaseClosed || rec.Plan.Outcome != OutcomeDone {
			continue
		}
		for _, r := range rec.Plan.Deferred {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// Deps are the collaborators of a Closer. Prepare needs Roles, NewRedactor,
// Changed, BaseFile and StagingDir; the rest are for Closer.
type Deps struct {
	Effects   Effects
	Comments  Comments
	Committer Committer
	Store     Store
	Roles     RoleSet
	// NewRedactor builds the redactor for a job, with the executor's secret
	// values (rebuilt, never persisted). Resume does not call it: it uses
	// only already-redacted staged bytes.
	NewRedactor func(Job) (Redactor, error)
	// Changed lists paths under a pathspec changed since a base commit
	// (gitx.Git.ChangedSince).
	Changed func(ctx context.Context, worktree, base string, paths ...string) ([]gitx.PathChange, error)
	// BaseFile reads one file at a commit (gitx.Git.FileAt): ok is false when
	// the path does not exist there. Prepare uses it to compare plan.md with
	// its base.
	BaseFile   func(ctx context.Context, worktree, rev, path string, maxBytes int64) (data []byte, ok bool, err error)
	Repo       github.Repo
	StagingDir string
	Log        *slog.Logger
	Now        func() time.Time
}

func (d Deps) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Closer executes close-out plans.
type Closer struct {
	d    Deps
	mu   sync.Mutex
	busy map[string]bool // jobs with an advance in flight
}

// New returns a Closer.
func New(d Deps) *Closer { return &Closer{d: d, busy: map[string]bool{}} }

// acquire marks a job busy; it reports false when another Run or Resume
// already holds it. Without it a concurrent list-then-create would double-post.
func (c *Closer) acquire(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.busy == nil {
		c.busy = map[string]bool{}
	}
	if c.busy[id] {
		return false
	}
	c.busy[id] = true
	return true
}

func (c *Closer) release(id string) {
	c.mu.Lock()
	delete(c.busy, id)
	c.mu.Unlock()
}

// checkDeps refuses a live Closer that cannot look for its own comment.
func (c *Closer) checkDeps() error {
	if c.d.Comments == nil && !c.d.Effects.DryRun() {
		return errors.New("closeout: Deps.Comments is required outside dry-run (a replay would post twice)")
	}
	return nil
}

// Run closes out j. If the job already has a record it resumes that one and
// never reads the worktree again.
func (c *Closer) Run(ctx context.Context, j Job) (Outcome, error) {
	if err := j.validate(); err != nil {
		return "", err
	}
	if err := c.checkDeps(); err != nil {
		return "", err
	}
	if !c.acquire(j.ID) {
		return "", fmt.Errorf("closeout job %s: %w", j.ID, ErrBusy)
	}
	defer c.release(j.ID)
	sj, err := c.d.Store.Job(ctx, j.ID)
	if err != nil {
		return "", fmt.Errorf("closeout job %s: load job: %w", j.ID, err)
	}
	if sj.CloseoutPhase != "" && len(sj.CloseoutPlan) > 0 {
		rec, err := decode(sj)
		if err != nil {
			return "", err
		}
		return c.advance(ctx, rec)
	}
	start := c.d.now()
	rec, err := Prepare(ctx, j, c.d)
	if err != nil {
		return "", fmt.Errorf("closeout job %s: prepare: %w", j.ID, err)
	}
	if err := c.save(ctx, rec); err != nil {
		return "", err
	}
	out, err := c.advance(ctx, rec)
	if err == nil {
		c.d.log().Info("closeout", "job", j.ID, "issue", j.Issue, "stage", j.Stage, "role", j.Role,
			"outcome", out, "skipped", len(rec.Plan.Skipped), "dur", c.d.now().Sub(start))
	}
	return out, err
}

// ResumeJob finishes the open close-out of one job. A job with no open phase
// is state.ErrNotFound (wrapped); one whose close-out another goroutine is
// advancing is ErrBusy. The Reaper uses it for jobs nobody is driving.
func (c *Closer) ResumeJob(ctx context.Context, id string) (Outcome, error) {
	if err := c.checkDeps(); err != nil {
		return "", err
	}
	if !c.acquire(id) {
		return "", fmt.Errorf("closeout job %s: %w", id, ErrBusy)
	}
	defer c.release(id)
	// Read after taking the lock: a Run may have closed the job meanwhile.
	sj, err := c.d.Store.Job(ctx, id)
	if err != nil {
		return "", fmt.Errorf("closeout job %s: load job: %w", id, err)
	}
	if sj.CloseoutPhase == "" || sj.CloseoutPhase == string(PhaseClosed) || len(sj.CloseoutPlan) == 0 {
		return "", fmt.Errorf("closeout job %s: no open close-out: %w", id, state.ErrNotFound)
	}
	rec, err := decode(sj)
	if err != nil {
		return "", err
	}
	c.d.log().Info("closeout.resume", "job", id, "issue", rec.Job.Issue, "phase", rec.Phase)
	return c.advance(ctx, rec)
}

// Resume finishes every close-out that was started and not closed.
func (c *Closer) Resume(ctx context.Context) error {
	if err := c.checkDeps(); err != nil {
		return err
	}
	jobs, err := c.d.Store.OpenCloseouts(ctx)
	if err != nil {
		return fmt.Errorf("closeout resume: %w", err)
	}
	var errs []error
	for _, sj := range jobs {
		rec, err := decode(sj)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !c.acquire(rec.Job.ID) {
			c.d.log().Info("closeout.resume skipped: in flight", "job", rec.Job.ID)
			continue
		}
		c.d.log().Info("closeout.resume", "job", rec.Job.ID, "issue", rec.Job.Issue, "phase", rec.Phase)
		_, err = c.advance(ctx, rec)
		c.release(rec.Job.ID)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func decode(sj state.Job) (Record, error) {
	var rec Record
	if err := json.Unmarshal(sj.CloseoutPlan, &rec); err != nil {
		return Record{}, fmt.Errorf("closeout job %s: decode record: %w", sj.ID, err)
	}
	if rec.Version != recordVersion {
		return Record{}, fmt.Errorf("closeout job %s: record version %d, want %d", sj.ID, rec.Version, recordVersion)
	}
	return rec, nil
}

func (c *Closer) save(ctx context.Context, rec Record) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("closeout job %s: encode record: %w", rec.Job.ID, err)
	}
	if err := c.d.Store.SetCloseout(ctx, rec.Job.ID, string(rec.Phase), b); err != nil {
		return fmt.Errorf("closeout job %s phase %s: save: %w", rec.Job.ID, rec.Phase, err)
	}
	return nil
}

func (c *Closer) fail(rec Record, err error) error {
	err = fmt.Errorf("closeout job %s phase %s: %w", rec.Job.ID, rec.Phase, err)
	c.d.log().Error("closeout failed", "job", rec.Job.ID, "issue", rec.Job.Issue, "phase", rec.Phase, "error", err)
	return err
}

// advance runs the steps after rec.Phase.
func (c *Closer) advance(ctx context.Context, rec Record) (Outcome, error) {
	if rec.Phase == PhaseClosed {
		return rec.Plan.Outcome, nil
	}
	if rec.Phase == PhasePrepared {
		if err := c.commit(ctx, &rec); err != nil {
			return "", c.fail(rec, err)
		}
		rec.Phase = PhaseCommitted
		if err := c.save(ctx, rec); err != nil {
			return "", err
		}
	}
	if rec.Phase == PhaseCommitted {
		if err := c.request(ctx, rec); err != nil {
			return "", c.fail(rec, err)
		}
		rec.Phase = PhaseRequested
		if err := c.save(ctx, rec); err != nil {
			return "", err
		}
	}
	if rec.Phase == PhaseRequested {
		if err := c.comment(ctx, &rec); err != nil {
			return "", c.fail(rec, err)
		}
		rec.Phase = PhaseCommented
		if err := c.save(ctx, rec); err != nil {
			return "", err
		}
	}
	if err := c.finish(ctx, rec); err != nil {
		return "", c.fail(rec, err)
	}
	rec.Phase = PhaseClosed
	if err := c.save(ctx, rec); err != nil {
		return "", err
	}
	if err := os.RemoveAll(filepath.Join(c.d.StagingDir, rec.Job.ID)); err != nil {
		c.d.log().Warn("closeout: remove staging", "job", rec.Job.ID, "error", err)
	}
	if rec.Synthetic || c.d.Effects.DryRun() {
		c.d.log().Info("closeout: raw log kept (dry-run)", "job", rec.Job.ID)
	} else {
		c.dropRawLog(rec)
	}
	return rec.Plan.Outcome, nil
}

// commit hands the staged files to the committer. A permanent error (the
// committer already retried) turns the job into a failure with no files.
func (c *Closer) commit(ctx context.Context, rec *Record) error {
	p := &rec.Plan
	if len(p.Files) == 0 {
		return nil
	}
	req := committer.Request{Issue: rec.Job.Issue, Stage: rec.Job.Stage, Role: rec.Job.Role,
		Summary: commitSummary(*p), JobID: rec.Job.ID}
	for _, f := range p.Files {
		cf := committer.File{Path: f.Path, Delete: f.Delete}
		if !f.Delete {
			cf.Src = f.Staged
		}
		req.Files = append(req.Files, cf)
	}
	res, err := c.d.Committer.Commit(ctx, rec.Job.Branch, req)
	switch {
	case err == nil:
		rec.SHA, rec.Synthetic = res.SHA, res.Synthetic
		return nil
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return err
	}
	c.d.log().Warn("closeout: commit failed", "job", rec.Job.ID, "error", err)
	if p.Outcome == OutcomeRetry {
		// The log is evidence, not the result: a retry must not turn into a
		// failure because it could not be committed (a worker that stopped
		// before its first push has no branch to commit to).
		p.Files, p.LogPath = nil, ""
		p.Reason += "; log not committed: " + err.Error()
		return nil
	}
	tail, tailWithheld, keep := p.Tail, p.TailWithheld, p.KeepUntil
	*p = failedPlan(rec.Job, "commit failed: "+err.Error(), p.LogWithheld)
	p.Tail, p.TailWithheld, p.KeepUntil = tail, tailWithheld, keep
	return nil
}

func commitSummary(p Plan) string {
	for _, s := range []string{p.Summary, p.Reason} {
		if l := strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0]); l != "" {
			return l
		}
	}
	return "close-out " + string(p.Outcome)
}

// request applies the next labels, grouped by target number.
func (c *Closer) request(ctx context.Context, rec Record) error {
	return c.addGrouped(ctx, rec.Plan.Requests)
}

func (c *Closer) addGrouped(ctx context.Context, ls []Label) error {
	var order []int
	by := map[int][]string{}
	for _, l := range ls {
		if _, ok := by[l.Number]; !ok {
			order = append(order, l.Number)
		}
		by[l.Number] = append(by[l.Number], l.Name)
	}
	for _, n := range order {
		if err := c.d.Effects.AddLabels(ctx, n, by[n]...); err != nil {
			return fmt.Errorf("add labels on #%d: %w", n, err)
		}
	}
	return nil
}

// comment posts the summary once; the marker makes a replay find it.
func (c *Closer) comment(ctx context.Context, rec *Record) error {
	n := rec.Job.target()
	if n > 0 && c.d.Comments != nil {
		cs, err := c.d.Comments.ListComments(ctx, n, time.Time{})
		if err != nil {
			return fmt.Errorf("list comments on #%d: %w", n, err)
		}
		for _, cm := range cs {
			if strings.HasPrefix(cm.Body, markerFor(rec.Job.ID)) {
				rec.CommentID = cm.ID
				return nil
			}
		}
	}
	cm, err := c.d.Effects.CreateComment(ctx, n, RenderComment(*rec, c.d.Repo, c.d.Effects.DryRun()))
	if err != nil {
		return fmt.Errorf("comment on #%d: %w", n, err)
	}
	rec.CommentID = cm.ID
	return nil
}

// finish swaps the labels (":running" is removed last, so it marks the commit
// point) and closes the job in state.
func (c *Closer) finish(ctx context.Context, rec Record) error {
	p := rec.Plan
	if err := c.addGrouped(ctx, p.Terminal); err != nil {
		return err
	}
	for _, l := range p.Remove {
		if err := c.d.Effects.RemoveLabel(ctx, l.Number, l.Name); err != nil {
			return fmt.Errorf("remove %s on #%d: %w", l.Name, l.Number, err)
		}
	}
	var cost *float64
	if rec.Job.CostUSD > 0 {
		v := rec.Job.CostUSD
		cost = &v
	}
	keep := p.KeepUntil
	if keep.IsZero() {
		keep = c.d.now().Add(failedKeep)
	}
	var err error
	switch p.Outcome {
	case OutcomeFailed:
		err = c.d.Store.Fail(ctx, rec.Job.ID, clip(p.Reason, maxReasonRunes), keep)
	case OutcomeRetry:
		err = c.d.Store.FailRetry(ctx, rec.Job.ID, clip(p.Reason, maxReasonRunes), keep)
	default:
		err = c.d.Store.Complete(ctx, rec.Job.ID, cost)
	}
	if err != nil {
		return fmt.Errorf("close job in state: %w", err)
	}
	return nil
}

// dropRawLog deletes the unredacted host log once its redacted copy has been
// committed. It is kept, and the keep is logged, when redaction failed or the
// scan flagged it (so a human can inspect it) or when the commit did not
// carry it.
func (c *Closer) dropRawLog(rec Record) {
	j := rec.Job
	if j.LogPath == "" {
		return
	}
	if rec.Plan.LogPath == "" {
		c.d.log().Warn("closeout: raw log kept", "job", j.ID, "path", j.LogPath, "withheld", rec.Plan.LogWithheld)
		return
	}
	if err := os.Remove(j.LogPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		c.d.log().Warn("closeout: remove raw log", "job", j.ID, "path", j.LogPath, "error", err)
	}
}
