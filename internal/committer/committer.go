// Package committer is the only code that asks for a commit or a push of
// ard-owned files. A caller hands it a Request (one issue, one stage:role, a
// file set) and gets a Result synchronously. Each target branch has one lane
// that runs one request at a time; main has a persistent lane (Committer) and
// Branches creates one per PR branch.
//
// Local git work (fetch, worktree, staging) goes through gitx. gitx serialises
// worktree add/remove/prune per repository across all lanes and the executor
// (#109); fetch, commit and push stay parallel. The two steps
// that change history or the remote, commit and push, go through the Effects
// interface so --dry-run logs them instead of acting.
//
// Retry: a request is a snapshot of whole files on one commit. When a push is
// rejected the lane fetches, re-applies the snapshot on the new upstream head
// and pushes again, and fails with ErrConflict if upstream touched one of the
// request's paths. That is what `git rebase` would produce, without ever
// leaving rebase state behind.
//
// Branches.CommitCode is the only path for worker code. It commits only paths
// CheckCodePath accepts, only to worker/ branches, never to main. The
// .ard/<issue#>/ lane (Commit, CheckPath) is unchanged.
//
// Commit messages, PR titles and PR bodies that embed untrusted text must pass
// it through NeutraliseRefs so it cannot close an issue. ard's own
// "Closes #<issue>" line in a worker PR body is written by the caller after
// neutralising, never fed through it.
package committer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
)

// maxPushes is one push plus up to 3 retries.
const maxPushes = 4

// Effects is the subset of effects.Effects the committer needs.
type Effects interface {
	Commit(ctx context.Context, op effects.CommitOp) (effects.CommitResult, error)
	Push(ctx context.Context, op effects.PushOp) error
	DryRun() bool
}

// Config configures a Committer or Branches.
type Config struct {
	RepoDir   string          // local clone that provides the object store
	WorkDir   string          // lane worktrees live under it
	RemoteURL string          // https://github.com/<owner>/<repo>.git; file:// in tests
	Branch    string          // NewMain only; default "main"
	Bot       github.Identity // author and committer; Login is "<slug>[bot]", ID the bot user id
	Token     gitx.TokenFunc  // nil = no auth header (file remotes in tests)
	// SkipCI appends " [skip ci]" to commit messages. Decided on #17: main
	// `.ard/<n>/` commits skip CI; PR-branch commits never do, so required
	// checks run and report on the head the merger needs. nil selects that
	// default for the lane; set it only to override (tests).
	SkipCI  *bool
	Backoff func(attempt int) time.Duration
	Log     *slog.Logger
}

type core struct {
	cfg    Config
	fx     Effects
	g      *gitx.Git
	author gitx.Identity
	skipCI bool
	log    *slog.Logger
}

func newCore(cfg Config, fx Effects, g *gitx.Git, defaultSkipCI bool) (*core, error) {
	if cfg.RepoDir == "" || cfg.WorkDir == "" || cfg.RemoteURL == "" {
		return nil, fmt.Errorf("%w: RepoDir, WorkDir and RemoteURL are required", ErrInvalid)
	}
	u, err := url.Parse(cfg.RemoteURL)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: RemoteURL: %v", ErrInvalid, err)
	case u.Scheme == "https" && u.Host != "":
	case u.Scheme == "file" && cfg.Token == nil: // local test remotes only; never with a credential
	default:
		return nil, fmt.Errorf("%w: RemoteURL must be https (file only without a Token)", ErrInvalid)
	}
	if g == nil {
		g = gitx.New("")
	}
	skip := defaultSkipCI
	if cfg.SkipCI != nil {
		skip = *cfg.SkipCI
	}
	c := &core{cfg: cfg, fx: fx, g: g, skipCI: skip, log: cfg.Log}
	if c.log == nil {
		c.log = slog.Default()
	}
	if cfg.Bot.Login != "" && cfg.Bot.ID != 0 {
		c.author = gitx.Identity{Name: cfg.Bot.Login, Email: cfg.Bot.NoreplyEmail()}
	} else if !fx.DryRun() {
		return nil, fmt.Errorf("%w: bot identity (login and user id) is required", ErrInvalid)
	} else {
		c.author = gitx.Identity{Name: "ard[bot]", Email: "0+ard[bot]@users.noreply.github.com"}
	}
	return c, nil
}

func (c *core) backoff(attempt int) time.Duration {
	if c.cfg.Backoff != nil {
		return c.cfg.Backoff(attempt)
	}
	return 500 * time.Millisecond * time.Duration(attempt)
}

func upstreamRef(branch string) string { return "refs/ard/upstream/" + branch }

// lane serialises requests for one branch.
type lane struct {
	*core
	branch     string
	wt         string
	persistent bool // main: worktree lives as long as the lane
	sem        chan struct{}
	mu         sync.Mutex
	closed     bool
}

func newLane(c *core, branch, wt string, persistent bool) *lane {
	return &lane{core: c, branch: branch, wt: wt, persistent: persistent, sem: make(chan struct{}, 1)}
}

func (l *lane) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

// commit validates, waits for the lane and runs the pipeline.
func (l *lane) commit(ctx context.Context, req Request) (res Result, err error) {
	if l.isClosed() {
		return Result{}, ErrClosed
	}
	issue := req.Issue
	dry := l.fx.DryRun()
	switch {
	case issue < 0 && dry:
		issue = -issue
	case issue <= 0:
		return Result{}, fmt.Errorf("commit issue %d: %w: issue must be positive", req.Issue, &GuardError{Paths: []string{""}, Reasons: []string{"non-positive issue"}})
	}
	snap, err := snapshot(req, issue)
	if err != nil {
		return Result{}, fmt.Errorf("commit issue %d (%s:%s): %w", req.Issue, req.Stage, req.Role, err)
	}
	msg, err := Message(req.Stage, req.Role, issue, req.Summary, req.JobID, l.skipCI)
	if err != nil {
		return Result{}, fmt.Errorf("commit issue %d (%s:%s): %w", req.Issue, req.Stage, req.Role, err)
	}
	start := time.Now()
	defer func() {
		attrs := []any{"issue", req.Issue, "stage", req.Stage, "role", req.Role, "branch", l.branch, "job", req.JobID,
			"files", len(snap.Files), "duration", time.Since(start).String()}
		if err != nil {
			l.log.WarnContext(ctx, "committer.failed", append(attrs, "err", err.Error())...)
			return
		}
		l.log.InfoContext(ctx, "committer.commit", append(attrs, "sha", res.SHA, "noop", res.NoOp, "synthetic", res.Synthetic, "attempts", res.Attempts)...)
	}()
	if req.Issue < 0 { // synthetic issue: validated, nothing touches git
		return Result{SHA: strings.Repeat("0", 40), Synthetic: true}, nil
	}

	select {
	case l.sem <- struct{}{}:
	case <-ctx.Done():
		return Result{}, fmt.Errorf("commit issue %d: %w", req.Issue, ctx.Err())
	}
	defer func() { <-l.sem }()
	if l.isClosed() {
		return Result{}, ErrClosed
	}
	res, err = l.run(ctx, snapOf(snap, msg))
	if err != nil {
		err = fmt.Errorf("commit issue %d (%s:%s): %w", req.Issue, req.Stage, req.Role, err)
	}
	return res, err
}

// snapFile is one validated change, ready to apply.
type snapFile struct {
	path string
	data []byte
	exec bool
	del  bool
}

// snap is a validated request in the form the lane applies, whichever lane
// (.ard or worker code) produced it.
type snap struct {
	files    []snapFile
	declared []string
	msg      string
	// base, when set, is the commit the caller's content was derived from:
	// the first attempt refuses to overwrite a declared path that changed
	// between base and the fetched head.
	base string
}

func (s snap) wantMode(path string) string {
	for _, f := range s.files {
		if f.path == path {
			if f.del {
				return "000000"
			}
			return modeOf(f.exec)
		}
	}
	return ""
}

// snapOf converts a validated .ard Request. Its files are never executable.
func snapOf(req Request, msg string) snap {
	s := snap{msg: msg, declared: make([]string, len(req.Files))}
	for i, f := range req.Files {
		s.declared[i] = f.Path
		s.files = append(s.files, snapFile{path: f.Path, data: f.Data, del: f.Delete})
	}
	return s
}

func (l *lane) run(ctx context.Context, s snap) (Result, error) {
	if !l.persistent {
		if err := l.addWorktree(ctx); err != nil {
			return Result{}, err
		}
		defer func() { _ = l.g.WorktreeRemove(context.WithoutCancel(ctx), l.cfg.RepoDir, l.wt) }()
	}
	declared := s.declared

	var base, prev string
	for attempt := 1; ; attempt++ {
		var err error
		if base, err = l.fetch(ctx); err != nil {
			l.reset(ctx, prev)
			return Result{Attempts: attempt - 1}, err
		}
		from := prev
		if attempt == 1 {
			from = s.base
		}
		if from != "" {
			changed, err := l.g.ChangedPaths(ctx, l.wt, from, base, declared...)
			if err != nil {
				l.reset(ctx, base)
				return Result{Attempts: attempt - 1}, err
			}
			if len(changed) > 0 {
				l.reset(ctx, base)
				return Result{Attempts: attempt - 1}, fmt.Errorf("%w: %s", ErrConflict, strings.Join(changed, ", "))
			}
		}
		res, retry, err := l.attempt(ctx, s, base, false)
		res.Attempts = attempt
		if err == nil {
			if res.NoOp || res.Synthetic {
				l.reset(ctx, base)
			}
			return res, nil
		}
		l.reset(ctx, base)
		if !retry || attempt >= maxPushes {
			return res, err
		}
		l.log.DebugContext(ctx, "committer.retry", "branch", l.branch, "attempt", attempt, "err", err.Error())
		prev = base
		select {
		case <-time.After(l.backoff(attempt)):
		case <-ctx.Done():
			return res, ctx.Err()
		}
	}
}

// attempt applies the snapshot on base, commits and pushes. retry reports
// whether a failure is worth another fetch-and-apply round. With create, base
// is the commit a new branch is cut from: nothing staged is ErrInvalid (a
// branch with no commit cannot become a PR) and the push's lease expects the
// ref to be absent, so a branch that exists is ErrBranchExists, never retried.
func (l *lane) attempt(ctx context.Context, s snap, base string, create bool) (res Result, retry bool, err error) {
	if err := l.g.ResetHard(ctx, l.wt, base); err != nil {
		return res, false, err
	}
	if err := l.g.CleanAll(ctx, l.wt); err != nil {
		return res, false, err
	}
	declared, msg := s.declared, s.msg
	var writes, deletes, execs []string
	for _, f := range s.files {
		full := filepath.Join(l.wt, filepath.FromSlash(f.path))
		// Defence in depth behind CheckCodePath/CheckPath: never touch a
		// path that resolves outside the worktree.
		if rel, err := filepath.Rel(l.wt, full); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			ge := &GuardError{}
			ge.add(f.path, "escapes the worktree")
			return res, false, ge
		}
		if err := checkParents(l.wt, f.path); err != nil {
			return res, false, err
		}
		if f.del {
			if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
				return res, false, fmt.Errorf("remove %s: %w", f.path, err)
			}
			deletes = append(deletes, f.path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return res, false, fmt.Errorf("mkdir for %s: %w", f.path, err)
		}
		if err := os.WriteFile(full, f.data, 0o644); err != nil {
			return res, false, fmt.Errorf("write %s: %w", f.path, err)
		}
		// WriteFile keeps the mode of an existing file, so set it explicitly.
		if err := os.Chmod(full, fileMode(f.exec)); err != nil {
			return res, false, fmt.Errorf("chmod %s: %w", f.path, err)
		}
		writes = append(writes, f.path)
		if f.exec {
			execs = append(execs, f.path)
		}
	}
	if len(writes) > 0 {
		if err := l.g.AddForce(ctx, l.wt, writes...); err != nil {
			return res, false, err
		}
	}
	if len(deletes) > 0 {
		if err := l.g.RmCached(ctx, l.wt, deletes...); err != nil {
			return res, false, err
		}
	}
	if len(execs) > 0 {
		if err := l.g.SetExec(ctx, l.wt, execs...); err != nil {
			return res, false, err
		}
	}
	changes, err := l.g.StagedChanges(ctx, l.wt)
	if err != nil {
		return res, false, err
	}
	if err := CheckStaged(declared, changes); err != nil {
		return res, false, err
	}
	if err := checkModes(s, changes); err != nil {
		return res, false, err
	}
	nothing := func() (Result, bool, error) {
		if create {
			return res, false, fmt.Errorf("%w: nothing to commit on a new branch", ErrInvalid)
		}
		return Result{SHA: base, NoOp: true}, false, nil
	}
	if len(changes) == 0 {
		return nothing()
	}
	cr, err := l.fx.Commit(ctx, effects.CommitOp{Dir: l.wt, Message: msg, Author: l.author})
	if errors.Is(err, effects.ErrNothingToCommit) {
		return nothing()
	}
	if err != nil {
		return res, false, err
	}
	lease := base
	if create {
		lease = zeroSHA
	}
	err = l.fx.Push(ctx, effects.PushOp{Dir: l.wt, Remote: l.cfg.RemoteURL, Ref: "HEAD:refs/heads/" + l.branch,
		ForceWithLease: lease, Token: l.cfg.Token})
	if create && (errors.Is(err, effects.ErrNonFastForward) || errors.Is(err, effects.ErrStaleLease)) {
		return res, false, fmt.Errorf("%w: %v", ErrBranchExists, err)
	}
	if err != nil {
		return res, errors.Is(err, effects.ErrNonFastForward) || errors.Is(err, effects.ErrStaleLease), err
	}
	return Result{SHA: cr.SHA, Synthetic: cr.Synthetic}, false, nil
}

// fetch updates the private upstream ref and returns its SHA.
func (l *lane) fetch(ctx context.Context) (string, error) {
	spec := "+refs/heads/" + l.branch + ":" + upstreamRef(l.branch)
	if err := l.g.FetchRef(ctx, l.cfg.RepoDir, l.cfg.RemoteURL, spec, l.cfg.Token); err != nil {
		if errors.Is(err, gitx.ErrRefNotFound) && !l.persistent {
			return "", fmt.Errorf("%w: %s", ErrBranchGone, l.branch)
		}
		return "", err
	}
	return l.g.RevParse(ctx, l.cfg.RepoDir, upstreamRef(l.branch))
}

// reset returns the worktree to base so the next request starts clean. It
// uses a fresh context because the request's may be cancelled.
func (l *lane) reset(ctx context.Context, base string) {
	if base == "" {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := l.g.ResetHard(rctx, l.wt, base); err != nil {
		l.log.WarnContext(ctx, "committer.reset_failed", "branch", l.branch, "err", err.Error())
		return
	}
	if err := l.g.CleanAll(rctx, l.wt); err != nil {
		l.log.WarnContext(ctx, "committer.reset_failed", "branch", l.branch, "err", err.Error())
	}
}

// addWorktree replaces whatever is at l.wt with a clean detached worktree of
// the freshly fetched upstream head.
func (l *lane) addWorktree(ctx context.Context) error {
	if err := os.MkdirAll(l.cfg.WorkDir, 0o755); err != nil {
		return fmt.Errorf("mkdir workdir: %w", err)
	}
	if _, err := l.fetch(ctx); err != nil {
		return err
	}
	if err := l.g.WorktreeRemove(ctx, l.cfg.RepoDir, l.wt); err != nil {
		return err
	}
	return l.g.WorktreeAdd(ctx, l.cfg.RepoDir, l.wt, upstreamRef(l.branch))
}

// checkParents refuses to write through a symlinked directory.
func checkParents(root, rel string) error {
	dir := root
	parts := strings.Split(rel, "/")
	for _, seg := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, seg)
		fi, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lstat %s: %w", dir, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			ge := &GuardError{}
			ge.add(rel, "parent directory is a symlink or not a directory")
			return ge
		}
	}
	if fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		ge := &GuardError{}
		ge.add(rel, "target is a symlink")
		return ge
	}
	return nil
}

// close waits for the in-flight request, then optionally removes the worktree.
func (l *lane) close(ctx context.Context) error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	select {
	case l.sem <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("close committer: %w", ctx.Err())
	}
	defer func() { <-l.sem }()
	if l.persistent {
		return l.g.WorktreeRemove(ctx, l.cfg.RepoDir, l.wt)
	}
	return nil
}

// Committer is the lane for the main branch.
type Committer struct{ l *lane }

// NewMain prepares the persistent main worktree. A leftover or dirty
// directory from a crash is replaced.
func NewMain(ctx context.Context, cfg Config, fx Effects, g *gitx.Git) (*Committer, error) {
	c, err := newCore(cfg, fx, g, true)
	if err != nil {
		return nil, err
	}
	major, minor, err := c.g.Version(ctx)
	if err != nil {
		return nil, err
	}
	if major < 2 || (major == 2 && minor < 32) {
		return nil, fmt.Errorf("git %d.%d is too old: 2.32 or later is required", major, minor)
	}
	branch := cfg.Branch
	if branch == "" {
		branch = "main"
	}
	l := newLane(c, branch, filepath.Join(cfg.WorkDir, "committer-main"), true)
	if err := l.addWorktree(ctx); err != nil {
		return nil, fmt.Errorf("prepare committer worktree: %w", err)
	}
	return &Committer{l: l}, nil
}

// Commit commits req to the main branch and pushes it.
func (c *Committer) Commit(ctx context.Context, req Request) (Result, error) {
	return c.l.commit(ctx, req)
}

// Close waits for the in-flight request and removes the worktree. Later
// Commit calls return ErrClosed.
func (c *Committer) Close(ctx context.Context) error { return c.l.close(ctx) }

func fileMode(exec bool) os.FileMode {
	if exec {
		return 0o755
	}
	return 0o644
}

// checkModes verifies every staged entry has the mode the request asked for.
func checkModes(s snap, changes []gitx.Change) error {
	ge := &GuardError{}
	for _, c := range changes {
		if want := s.wantMode(c.Path); c.NewMode != want {
			ge.add(c.Path, fmt.Sprintf("staged mode %s, want %s", c.NewMode, want))
		}
	}
	if len(ge.Paths) > 0 {
		return ge
	}
	return nil
}
