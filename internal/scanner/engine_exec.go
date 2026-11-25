package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/albertcbrown/ard/internal/labels"
	"log/slog"
	"path"
	"path/filepath"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

// Offerer grants job slots. *pool.Pool satisfies it.
type Offerer interface {
	Offer(loop string, ttl time.Duration, cands []pool.Candidate) []pool.Decision
}

// Jobs records scanner jobs in the controller database so a restarted
// controller adopts the slot. *state.Store satisfies it.
type Jobs interface {
	CreateJob(ctx context.Context, n state.NewJob) (state.Job, error)
	SetHandle(ctx context.Context, id string, h json.RawMessage) error
	Complete(ctx context.Context, id string, costUSD *float64) error
	Fail(ctx context.Context, id, reason string, keepUntil time.Time) error
}

// ExecDeps are the collaborators of ExecEngine.
type ExecDeps struct {
	Pool     func() Offerer // called at scan time: the pool does not exist when loops are built
	Executor executor.Executor
	Jobs     Jobs

	Repo    string // "owner/name"
	RepoDir string // abs path of the clone the job worktree is cut from

	OfferTTL time.Duration // how long a denied run's demand is remembered; default 10m
	Poll     time.Duration // Status poll interval; default 2s
	Now      func() time.Time
	Log      *slog.Logger
}

// ExecEngine runs a scanner's executable through the local executor in a pool
// slot of the scanner class, on a read-only worktree detached at the commit
// the tick's config was loaded from (Resolved.SHA), with the role directory
// taken from that commit's snapshot (Resolved.Repo). It does not fetch. It is
// the only engine in phase 0; the LLM engine arrives in phase 2.
type ExecEngine struct{ d ExecDeps }

var _ Engine = (*ExecEngine)(nil)

// NewExecEngine applies defaults to d.
func NewExecEngine(d ExecDeps) *ExecEngine {
	if d.OfferTTL <= 0 {
		d.OfferTTL = 10 * time.Minute
	}
	if d.Poll <= 0 {
		d.Poll = 2 * time.Second
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &ExecEngine{d: d}
}

// Scan asks the pool for a slot (ErrNoSlot when denied: the run is dropped,
// not queued), scans cfg.SHA and decodes stdout. The slot is released on
// every path. A non-zero exit, a timeout, a vanished process or any decode
// error is a *ScanError and its stdout is discarded.
func (x *ExecEngine) Scan(ctx context.Context, cfg *config.Resolved, role config.Role, since time.Time) (res ScanResult, err error) {
	d := x.d
	log := d.Log.With("scanner", role.Name)
	if cfg == nil || cfg.SHA == "" {
		return ScanResult{}, fmt.Errorf("scan %s: config has no commit to pin the scan to", role.Name)
	}
	sha := cfg.SHA
	if since.IsZero() {
		since = d.Now()
	}
	decs := d.Pool().Offer("scanners", d.OfferTTL, []pool.Candidate{{Class: pool.ClassScanner, Role: role.Name, Since: since, Priority: labels.DefaultPriority}})
	if len(decs) != 1 || decs[0].Verdict != pool.Granted || decs[0].Slot == nil {
		why := "no decision"
		if len(decs) == 1 {
			why = decs[0].Verdict.String() + ": " + decs[0].Reason
		}
		return ScanResult{}, fmt.Errorf("%w: %s", ErrNoSlot, why)
	}
	slot := decs[0].Slot
	defer slot.Release()

	res.SHA = sha

	job, err := d.Jobs.CreateJob(ctx, state.NewJob{Stage: "scanner", Role: role.Name})
	if err != nil {
		return ScanResult{SHA: sha}, fmt.Errorf("record scanner job: %w", err)
	}
	slot.SetJobID(job.ID)
	defer func() {
		bg := context.WithoutCancel(ctx)
		var jerr error
		if err == nil {
			jerr = d.Jobs.Complete(bg, job.ID, nil)
		} else {
			jerr = d.Jobs.Fail(bg, job.ID, err.Error(), time.Time{})
		}
		if jerr != nil {
			log.Error("scanner.job_record_failed", "job", job.ID, "error", jerr)
		}
	}()

	ej := executor.Job{
		ID: job.ID, Repo: d.Repo, Stage: "scanner", Role: role.Name,
		RepoDir: d.RepoDir, Ref: sha, ReadOnly: true, Timeout: role.Timeout,
		RoleDir:    filepath.Join(cfg.Repo, filepath.FromSlash(role.Dir)),
		RoleSource: executor.RoleSource{SHA: cfg.SHA, Path: role.Dir},
	}
	ej.TrustedInput = !role.ReadsUntrusted()
	ej.History = role.ReadsHistory()
	if !role.Network {
		ej.Network = executor.NetworkAdvisory
	}
	if role.Dockerfile != "" {
		ej.Dockerfile = filepath.Join(cfg.Repo, filepath.FromSlash(role.Dockerfile))
	}
	h, err := d.Executor.Prepare(ctx, ej)
	if err != nil {
		return ScanResult{SHA: sha}, fmt.Errorf("prepare scan: %w", err)
	}
	defer func() {
		if cerr := d.Executor.Cleanup(context.WithoutCancel(ctx), h, executor.CleanupOptions{}); cerr != nil {
			log.Warn("scanner.cleanup_failed", "job", job.ID, "error", cerr)
		}
	}()
	// No model key and no GitHub token reach the scan; it has network egress
	// for advisory databases and nothing else.
	h, err = d.Executor.Start(ctx, h, executor.Command{Path: filepath.Join(h.View.Role, path.Base(role.ScanPath))})
	if err != nil {
		return ScanResult{SHA: sha}, fmt.Errorf("start scan: %w", err)
	}
	if b, merr := json.Marshal(h); merr == nil {
		if herr := d.Jobs.SetHandle(ctx, job.ID, b); herr != nil {
			log.Warn("scanner.handle_not_saved", "job", job.ID, "error", herr)
		}
	}
	log.Info("scanner.scan_started", "job", job.ID, "sha", sha)

	st, err := executor.WaitFor(ctx, d.Executor, h, d.Poll)
	if err != nil {
		return ScanResult{SHA: sha}, fmt.Errorf("wait for scan: %w", err)
	}
	switch {
	case st.State == executor.TimedOut:
		return ScanResult{}, &ScanError{SHA: sha, TimedOut: true, Exit: st.ExitCode}
	case st.State == executor.Gone:
		return ScanResult{}, &ScanError{SHA: sha, Exit: -1, Err: fmt.Errorf("scan process vanished: %s", st.Reason)}
	case st.ExitCode != 0:
		return ScanResult{}, &ScanError{SHA: sha, Exit: st.ExitCode, Err: fmt.Errorf("exit status %d", st.ExitCode)}
	}
	// Stdout comes from its own file, never the combined log, which also
	// holds the scanner's stderr.
	rc, err := d.Executor.Logs(ctx, h, executor.LogsOptions{Stream: executor.Stdout})
	if err != nil {
		return ScanResult{SHA: sha}, fmt.Errorf("read scan output: %w", err)
	}
	defer rc.Close()
	fs, err := DecodeFindings(rc)
	var de DecodeErrors
	switch {
	case errors.As(err, &de):
		return ScanResult{}, &ScanError{SHA: sha, Decode: de}
	case err != nil:
		return ScanResult{SHA: sha}, err
	}
	return ScanResult{SHA: sha, Findings: fs}, nil
}
