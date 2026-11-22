// Package local is the executor that runs jobs on this host: a detached git
// worktree, then a host subprocess or an attached `docker run`.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/gitx"
)

// Options configures Local.
type Options struct {
	Root       string    // default $HOME/.ard
	Git        gitRunner // default: the git binary on PATH
	DockerBin  string    // default "docker"
	PathEnv    string    // PATH for host jobs; default the controller's PATH
	AllowEnv   []string  // model-key names; default ANTHROPIC_API_KEY, CLAUDE_CODE_OAUTH_TOKEN
	AllowFixed []string  // names allowed in Command.Fixed; default CLAUDE_CONFIG_DIR, DISABLE_AUTOUPDATER, CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC, CLAUDE_CODE_DISABLE_BACKGROUND_TASKS
	// ConfigDirEnv is the Fixed name whose value must equal View.ConfigDir,
	// the job's own config dir. Default CLAUDE_CONFIG_DIR.
	ConfigDirEnv string
	KillGrace    time.Duration // default 10s
	BuildTimeout time.Duration // default 15m
	Now          func() time.Time
	Log          *slog.Logger
	// OnHostUntrusted, if set, is called once per host-mode job that reads
	// untrusted input, right after the process starts. serve records an
	// events row through it. nil = log only.
	OnHostUntrusted func(ctx context.Context, h executor.Handle) error

	// Sidecar clone (see sidecar.go).
	SidecarGit     sidecarGit               // default gitx.New("")
	SidecarURL     func(repo string) string // default https://github.com/<repo>.git
	SidecarToken   gitx.TokenFunc           // nil = no auth header (local remotes in tests)
	SidecarTimeout time.Duration            // init + fetch + export bound; default 2m
}

// Local implements executor.Executor on this host.
type Local struct {
	root, dockerBin, pathEnv string
	git                      gitRunner
	allowEnv, allowFixed     map[string]bool
	configDirEnv             string
	grace, buildTimeout      time.Duration
	now                      func() time.Time
	log                      *slog.Logger
	onHostUntrusted          func(ctx context.Context, h executor.Handle) error

	sgit           sidecarGit
	sidecarURL     func(repo string) string
	sidecarToken   gitx.TokenFunc
	sidecarTimeout time.Duration

	mu           sync.Mutex
	tracks       map[string]*track
	sidecarLocks map[string]chan struct{} // per cache dir, 1-buffer slot; guarded by mu
}

var _ executor.Executor = (*Local)(nil)

func defaultPath() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return "/usr/local/bin:/usr/bin:/bin"
}

// New returns a Local with defaults applied.
func New(o Options) (*Local, error) {
	if o.Root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("find home: %w", err)
		}
		o.Root = filepath.Join(home, ".ard")
	}
	if !filepath.IsAbs(o.Root) {
		return nil, fmt.Errorf("root %q is not absolute", o.Root)
	}
	if o.Git == nil {
		o.Git = execGit{bin: "git"}
	}
	if o.DockerBin == "" {
		o.DockerBin = "docker"
	}
	if o.PathEnv == "" {
		o.PathEnv = defaultPath()
	}
	if o.AllowEnv == nil {
		o.AllowEnv = []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}
	}
	if o.AllowFixed == nil {
		o.AllowFixed = []string{"CLAUDE_CONFIG_DIR", "DISABLE_AUTOUPDATER", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"}
	}
	if o.ConfigDirEnv == "" {
		o.ConfigDirEnv = "CLAUDE_CONFIG_DIR"
	}
	if o.KillGrace <= 0 {
		o.KillGrace = 10 * time.Second
	}
	if o.BuildTimeout <= 0 {
		o.BuildTimeout = 15 * time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.SidecarGit == nil {
		o.SidecarGit = gitx.New("")
	}
	if o.SidecarURL == nil {
		o.SidecarURL = defaultSidecarURL
	}
	if o.SidecarTimeout <= 0 {
		o.SidecarTimeout = defaultSidecarTimeout
	}
	allow := map[string]bool{}
	for _, n := range o.AllowEnv {
		allow[n] = true
	}
	fixed := map[string]bool{}
	for _, n := range o.AllowFixed {
		fixed[n] = true
	}
	return &Local{configDirEnv: o.ConfigDirEnv, allowFixed: fixed, root: o.Root, dockerBin: o.DockerBin, pathEnv: o.PathEnv, git: o.Git,
		allowEnv: allow, grace: o.KillGrace, buildTimeout: o.BuildTimeout, now: o.Now, log: o.Log, onHostUntrusted: o.OnHostUntrusted,
		tracks: map[string]*track{}, sgit: o.SidecarGit, sidecarURL: o.SidecarURL, sidecarToken: o.SidecarToken,
		sidecarTimeout: o.SidecarTimeout}, nil
}

// configDirName is the per-job agent config dir inside the job home. Under
// HOME, the CLI's default ~/.claude resolves to the same fresh dir even if
// the config-dir variable were dropped.
const configDirName = ".claude"

// Prepare creates the worktree, writes inputs, creates log files and, for a
// Dockerfile role, ensures the image. It starts no process.
func (l *Local) Prepare(ctx context.Context, j executor.Job) (h executor.Handle, err error) {
	if err := j.Validate(); err != nil {
		return h, err
	}
	wt, err := l.WorktreePath(j.Repo, j.ID)
	if err != nil {
		return h, fmt.Errorf("prepare %s: %w", j.ID, err)
	}
	if _, err := os.Lstat(wt); err == nil {
		return h, fmt.Errorf("prepare %s: %w", j.ID, executor.ErrExists)
	}
	sha, err := l.git.RevParse(ctx, j.RepoDir, j.Ref)
	if err != nil {
		return h, fmt.Errorf("resolve ref %q: %w", j.Ref, err)
	}
	now := l.now().UTC()
	base := LogBase(j.Stage, j.Role, now)
	logDir, err := l.logDir(j.Repo, j.Issue)
	if err != nil {
		return h, fmt.Errorf("prepare %s: %w", j.ID, err)
	}
	home, err := l.homePath(j.Repo, j.ID)
	if err != nil {
		return h, fmt.Errorf("prepare %s: %w", j.ID, err)
	}
	h = executor.Handle{
		JobID: j.ID, Kind: "host", Issue: j.Issue, Stage: j.Stage, Role: j.Role,
		Worktree: wt, Home: home, RepoDir: j.RepoDir, BaseSHA: sha,
		LogPath: filepath.Join(logDir, base+".log"), StdoutPath: filepath.Join(logDir, base+".stdout"),
		View:    executor.Paths{Work: wt, Role: j.RoleDir},
		RoleDir: j.RoleDir, ReadOnly: j.ReadOnly,
		TrustedInput: j.TrustedInput, PreparedAt: now, Timeout: j.Timeout,
	}
	var created []string // paths to remove on failure
	worktreeAdded := false
	defer func() {
		if err == nil {
			return
		}
		if worktreeAdded {
			chmodWritable(wt)
			if rerr := l.git.WorktreeRemove(context.WithoutCancel(ctx), j.RepoDir, wt, true); rerr != nil {
				err = errors.Join(err, rerr)
			}
			os.RemoveAll(wt)
			l.git.WorktreePrune(context.WithoutCancel(ctx), j.RepoDir)
		}
		for _, p := range created {
			chmodWritable(p) // a sidecar tree under home is a-w
			os.RemoveAll(p)
		}
		h = executor.Handle{}
	}()

	if err = os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		return h, fmt.Errorf("create work dir: %w", err)
	}
	if err = l.git.WorktreeAdd(ctx, j.RepoDir, wt, sha); err != nil {
		return h, fmt.Errorf("add worktree: %w", err)
	}
	worktreeAdded = true
	if err = scrub(wt, j.Scrub); err != nil {
		return h, err
	}
	h.Scrubbed = append([]string(nil), j.Scrub...)
	if len(j.Inputs) > 0 {
		// os.Root refuses to follow symlinks out of the worktree, so a
		// committed symlink at an input path cannot redirect the write.
		var r *os.Root
		if r, err = os.OpenRoot(wt); err != nil {
			return h, fmt.Errorf("open worktree: %w", err)
		}
		defer r.Close()
		for _, f := range j.Inputs {
			rel := filepath.FromSlash(f.Path)
			if err = r.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
				return h, fmt.Errorf("write input %s: %w", f.Path, err)
			}
			if err = rejectSymlink(r, rel); err != nil {
				return h, fmt.Errorf("write input %s: %w", f.Path, err)
			}
			if err = r.WriteFile(rel, f.Data, 0o644); err != nil {
				return h, fmt.Errorf("write input %s: %w", f.Path, err)
			}
		}
	}
	if err = os.MkdirAll(filepath.Join(home, "tmp"), 0o700); err != nil {
		return h, fmt.Errorf("create home: %w", err)
	}
	created = append(created, home)
	// The config dir lives inside home, so Cleanup and this function's
	// failure path remove it with home. 0700 is forced past the umask.
	cfg := filepath.Join(home, configDirName)
	if err = os.Mkdir(cfg, 0o700); err != nil {
		return h, fmt.Errorf("create config dir: %w", err)
	}
	if err = os.Chmod(cfg, 0o700); err != nil {
		return h, fmt.Errorf("create config dir: %w", err)
	}
	h.View.ConfigDir = cfg
	// --- sidecar (#28): the executor clones, pins and mounts it read-only ---
	if j.Sidecar != (executor.Sidecar{}) {
		dest := filepath.Join(home, sidecarDirName)
		var scSHA string
		var fetched bool
		if scSHA, fetched, err = l.materialise(ctx, j.Repo, j.Sidecar, dest); err != nil {
			return h, err
		}
		h.SidecarDir, h.Sidecar, h.SidecarSHA = dest, j.Sidecar, scSHA
		h.View.Sidecar = dest
		l.log.Info("executor.sidecar_ready", "job", j.ID, "repo", j.Sidecar.Repo, "ref", j.Sidecar.Ref, "sha", scSHA, "fetched", fetched)
	}
	// --- end sidecar ---
	if err = os.MkdirAll(logDir, 0o700); err != nil {
		return h, fmt.Errorf("create log dir: %w", err)
	}
	for _, p := range []string{h.LogPath, h.StdoutPath} {
		var f *os.File
		if f, err = os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err != nil {
			return h, fmt.Errorf("create log %s: %w", p, err)
		}
		f.Close()
		created = append(created, p)
	}
	if h.SidecarDir != "" { // #28: read-only in both modes; Docker also mounts :ro
		if err = chmodReadOnly(h.SidecarDir); err != nil {
			return h, fmt.Errorf("make sidecar read-only: %w", err)
		}
	}
	if j.Dockerfile != "" {
		h.Kind = "docker"
		h.Container = "ard-" + j.ID
		if h.Image, err = l.ensureImage(ctx, j, h.LogPath); err != nil {
			return h, err
		}
		h.View = executor.Paths{Work: "/work", Role: "/role", ConfigDir: "/ard-config"}
		if h.SidecarDir != "" {
			h.View.Sidecar = "/sidecar"
		}
	} else if j.ReadOnly {
		// Last: nothing else may need to write into the worktree.
		if err = chmodReadOnly(wt); err != nil {
			return h, fmt.Errorf("make worktree read-only: %w", err)
		}
	}
	return h, nil
}

// chmodReadOnly removes write permission recursively (host mode ReadOnly).
func chmodReadOnly(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink != 0 {
			return err
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		return os.Chmod(p, st.Mode().Perm()&^0o222)
	})
}

// chmodWritable restores owner write permission so the tree can be removed.
func chmodWritable(root string) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if d == nil || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if st, e := d.Info(); e == nil {
			os.Chmod(p, st.Mode().Perm()|0o200|func() fs.FileMode {
				if d.IsDir() {
					return 0o100
				}
				return 0
			}())
		}
		return nil
	})
}

// Logs opens the selected log file.
func (l *Local) Logs(ctx context.Context, h executor.Handle, o executor.LogsOptions) (io.ReadCloser, error) {
	p := h.LogPath
	if o.Stream == executor.Stdout {
		p = h.StdoutPath
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	if o.TailLines <= 0 {
		return f, nil
	}
	defer f.Close()
	b, err := tailLines(f, o.TailLines)
	if err != nil {
		return nil, fmt.Errorf("tail log: %w", err)
	}
	return io.NopCloser(bytesReader(b)), nil
}

// Prune deletes host log files older than olderThan, then empty dirs.
func (l *Local) Prune(ctx context.Context, olderThan time.Duration) error {
	cutoff := l.now().Add(-olderThan)
	dir := filepath.Join(l.root, "logs")
	var dirs []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		if st, e := d.Info(); e == nil && st.ModTime().Before(cutoff) {
			return os.Remove(p)
		}
		return nil
	})
	for i := len(dirs) - 1; i > 0; i-- { // deepest first, keep logs/ itself
		os.Remove(dirs[i]) // fails harmlessly when not empty
	}
	if err != nil {
		return fmt.Errorf("prune logs: %w", err)
	}
	return nil
}

// scrub removes each path from the worktree: a regular file or a symlink
// (the link itself). A missing path is fine. It fails if the parent directory
// is a symlink (os.Root would follow it inside the worktree) and never removes
// a directory.
func scrub(wt string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	r, err := os.OpenRoot(wt)
	if err != nil {
		return fmt.Errorf("open worktree: %w", err)
	}
	defer r.Close()
	for _, p := range paths {
		rel := filepath.FromSlash(p)
		parent, err := r.Lstat(filepath.Dir(rel))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return fmt.Errorf("scrub %s: %w", p, err)
		case parent.Mode()&fs.ModeSymlink != 0 || !parent.IsDir():
			return fmt.Errorf("scrub %s: parent %s is not a plain directory", p, filepath.Dir(p))
		}
		st, err := r.Lstat(rel)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return fmt.Errorf("scrub %s: %w", p, err)
		case !st.Mode().IsRegular() && st.Mode()&fs.ModeSymlink == 0:
			return fmt.Errorf("scrub %s: not a file", p)
		}
		if err := r.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("scrub %s: %w", p, err)
		}
	}
	return nil
}

// rejectSymlink fails when rel already exists as a symlink: even a link that
// stays inside the worktree would let a branch swap what an input resolves to.
func rejectSymlink(r *os.Root, rel string) error {
	if st, err := r.Lstat(rel); err == nil && st.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", rel)
	}
	return nil
}
