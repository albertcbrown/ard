package committer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/gitx"
)

// InitBranch is the only branch Init commits to.
const InitBranch = "ard-init"

// InitMessage is the fixed commit message. It has no [skip ci], because CI
// must run on the PR.
const InitMessage = "ard(init): add starter .ard/ configuration"

// InitFile is one starter file. Exec commits it as 100755.
type InitFile struct {
	Path string // canonical repo-relative slash path; see CheckInitPath
	Data []byte
	Exec bool
}

var initPathRe = regexp.MustCompile(`^\.ard/(?:config\.yaml|(?:scanners|triage|workers|reviewers)/([a-z0-9][a-z0-9-]*)/[^/]+(?:/[^/]+)*)$`)

// CheckInitPath reports why p may not be proposed by ard init, or nil. It
// accepts exactly .ard/config.yaml and files under
// .ard/{scanners,triage,workers,reviewers}/<role>/, where <role> matches the
// role-name rule and is not all digits. It applies CheckPath's checks for
// empty, canonical, backslash, control characters and .git segments. It
// rejects .ard/<issue#>/ and everything outside .ard/.
func CheckInitPath(p string) error {
	if p == "" {
		return reason("empty path")
	}
	for _, r := range p {
		if r == '\\' || r < 0x20 || r == 0x7f {
			return reason("backslash or control character")
		}
	}
	if path.Clean(p) != p {
		return reason("not canonical")
	}
	for _, seg := range strings.Split(p, "/") {
		if strings.EqualFold(seg, ".git") {
			return reason("contains .git")
		}
	}
	m := initPathRe.FindStringSubmatch(p)
	if m == nil {
		return reason("not .ard/config.yaml or a file under a role directory")
	}
	if m[1] != "" && strings.Trim(m[1], "0123456789") == "" {
		return reason("numeric role name")
	}
	return nil
}

// Init proposes the starter tree on InitBranch, cut from the base branch
// (cfg.Branch, default "main"). Each Propose is one attempt: there is no
// retry loop and no force push. It is a separate lane from the main
// committer, which still writes only .ard/<issue#>/.
type Init struct {
	c    *core
	base string
	wt   string
}

// NewInit validates cfg and checks the git version the way NewMain does.
// cfg.Bot is required unless fx.DryRun().
func NewInit(ctx context.Context, cfg Config, fx Effects, g *gitx.Git) (*Init, error) {
	c, err := newCore(cfg, fx, g, false)
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
	base := cfg.Branch
	if base == "" {
		base = "main"
	}
	return &Init{c: c, base: base, wt: filepath.Join(cfg.WorkDir, "committer-init")}, nil
}

// Base fetches the base branch into refs/ard/upstream/<base> and returns its
// SHA. It returns ErrInitExists if InitBranch exists on the remote, and
// ErrBranchGone (naming the base) if the base branch does not exist.
func (i *Init) Base(ctx context.Context) (string, error) {
	cfg := i.c.cfg
	spec := "+refs/heads/" + i.base + ":" + upstreamRef(i.base)
	if err := i.c.g.FetchRef(ctx, cfg.RepoDir, cfg.RemoteURL, spec, cfg.Token); err != nil {
		if errors.Is(err, gitx.ErrRefNotFound) {
			return "", fmt.Errorf("%w: %s", ErrBranchGone, i.base)
		}
		return "", err
	}
	spec = "+refs/heads/" + InitBranch + ":" + upstreamRef(InitBranch)
	switch err := i.c.g.FetchRef(ctx, cfg.RepoDir, cfg.RemoteURL, spec, cfg.Token); {
	case err == nil:
		return "", ErrInitExists
	case !errors.Is(err, gitx.ErrRefNotFound):
		return "", err
	}
	return i.c.g.RevParse(ctx, cfg.RepoDir, upstreamRef(i.base))
}

// Propose commits files on top of base and pushes HEAD:refs/heads/ard-init as
// a normal push (ForceWithLease "", so the push creates the branch and never
// overwrites it). A non-fast-forward or stale-lease rejection returns
// ErrInitExists. If every file matches base, it returns Result{NoOp: true}
// and pushes nothing. The worktree is removed afterwards.
func (i *Init) Propose(ctx context.Context, base string, files []InitFile) (res Result, err error) {
	defer func() {
		attrs := []any{"branch", InitBranch, "base", base, "files", len(files)}
		if err != nil {
			i.c.log.WarnContext(ctx, "committer.init_failed", append(attrs, "err", err.Error())...)
			return
		}
		i.c.log.InfoContext(ctx, "committer.init", append(attrs, "sha", res.SHA, "noop", res.NoOp, "synthetic", res.Synthetic)...)
	}()
	declared, wantExec, err := validateInit(files)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(i.c.cfg.WorkDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("mkdir workdir: %w", err)
	}
	g, repo := i.c.g, i.c.cfg.RepoDir
	if err := g.WorktreeRemove(ctx, repo, i.wt); err != nil {
		return Result{}, err
	}
	if err := g.WorktreeAdd(ctx, repo, i.wt, base); err != nil {
		return Result{}, err
	}
	defer func() {
		if rerr := g.WorktreeRemove(context.WithoutCancel(ctx), repo, i.wt); rerr != nil {
			i.c.log.WarnContext(ctx, "committer.init_cleanup_failed", "err", rerr.Error())
		}
	}()

	var execPaths []string
	for _, f := range files {
		if err := checkParents(i.wt, f.Path); err != nil {
			return Result{}, err
		}
		full := filepath.Join(i.wt, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return Result{}, fmt.Errorf("mkdir for %s: %w", f.Path, err)
		}
		if err := os.WriteFile(full, f.Data, 0o644); err != nil {
			return Result{}, fmt.Errorf("write %s: %w", f.Path, err)
		}
		if f.Exec {
			if err := os.Chmod(full, 0o755); err != nil {
				return Result{}, fmt.Errorf("chmod %s: %w", f.Path, err)
			}
			execPaths = append(execPaths, f.Path)
		}
	}
	if err := g.AddForce(ctx, i.wt, declared...); err != nil {
		return Result{}, err
	}
	if len(execPaths) > 0 {
		if err := g.SetExec(ctx, i.wt, execPaths...); err != nil {
			return Result{}, err
		}
	}
	changes, err := g.StagedChanges(ctx, i.wt)
	if err != nil {
		return Result{}, err
	}
	if err := CheckStaged(declared, changes); err != nil {
		return Result{}, err
	}
	ge := &GuardError{}
	for _, c := range changes {
		if want := modeOf(wantExec[c.Path]); c.NewMode != want {
			ge.add(c.Path, fmt.Sprintf("staged mode %s, want %s", c.NewMode, want))
		}
	}
	if len(ge.Paths) > 0 {
		return Result{}, ge
	}
	if len(changes) == 0 {
		return Result{SHA: base, NoOp: true}, nil
	}
	cr, err := i.c.fx.Commit(ctx, effects.CommitOp{Dir: i.wt, Message: InitMessage, Author: i.c.author})
	if errors.Is(err, effects.ErrNothingToCommit) {
		return Result{SHA: base, NoOp: true}, nil
	}
	if err != nil {
		return Result{}, err
	}
	err = i.c.fx.Push(ctx, effects.PushOp{Dir: i.wt, Remote: i.c.cfg.RemoteURL, Ref: "HEAD:refs/heads/" + InitBranch,
		Token: i.c.cfg.Token})
	switch {
	case errors.Is(err, effects.ErrNonFastForward), errors.Is(err, effects.ErrStaleLease):
		return Result{}, fmt.Errorf("%w: %v", ErrInitExists, err)
	case err != nil:
		return Result{}, err
	}
	return Result{SHA: cr.SHA, Synthetic: cr.Synthetic, Attempts: 1}, nil
}

func modeOf(exec bool) string {
	if exec {
		return "100755"
	}
	return "100644"
}

// validateInit checks the file set and returns its sorted paths and the
// expected exec bit of each.
func validateInit(files []InitFile) (declared []string, wantExec map[string]bool, err error) {
	switch {
	case len(files) == 0:
		return nil, nil, fmt.Errorf("%w: no files", ErrInvalid)
	case len(files) > MaxFiles:
		return nil, nil, fmt.Errorf("%w: %d files, max %d", ErrInvalid, len(files), MaxFiles)
	}
	ge := &GuardError{}
	wantExec = make(map[string]bool, len(files))
	for _, f := range files {
		if perr := CheckInitPath(f.Path); perr != nil {
			ge.add(f.Path, perr.Error())
			continue
		}
		if _, dup := wantExec[f.Path]; dup {
			return nil, nil, fmt.Errorf("%w: duplicate path %q", ErrInvalid, f.Path)
		}
		if len(f.Data) > MaxFileBytes {
			ge.add(f.Path, "larger than 1 MiB")
			continue
		}
		wantExec[f.Path] = f.Exec
		declared = append(declared, f.Path)
	}
	if len(ge.Paths) > 0 {
		return nil, nil, ge
	}
	sort.Strings(declared)
	return declared, wantExec, nil
}
