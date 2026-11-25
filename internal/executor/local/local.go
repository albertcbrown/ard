// Package local is the executor that runs jobs on this host: a detached git
// worktree, then a host subprocess or an attached `docker run`.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/workspace"
	"github.com/albertcbrown/ard/internal/gitx"
)

// Options configures Local.
type Options struct {
	Root      string        // default $HOME/.ard
	Git       workspace.Git // default: the git binary on PATH
	DockerBin string        // default "docker"
	// AgentImage, if set, is the digest-pinned ref or local image ID that
	// runs every job that reads untrusted input and has no Dockerfile. A
	// role's own Dockerfile wins. "" = such jobs run on the host as before.
	AgentImage string
	PathEnv    string   // PATH for host jobs; default the controller's PATH
	AllowEnv   []string // model-key names; default ANTHROPIC_API_KEY, CLAUDE_CODE_OAUTH_TOKEN
	AllowFixed []string // names allowed in Command.Fixed; default CLAUDE_CONFIG_DIR, DISABLE_AUTOUPDATER, CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC, CLAUDE_CODE_DISABLE_BACKGROUND_TASKS
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
	SidecarGit     workspace.SidecarGit     // default gitx.New("")
	SidecarURL     func(repo string) string // default https://github.com/<repo>.git
	SidecarToken   gitx.TokenFunc           // nil = no auth header (local remotes in tests)
	SidecarTimeout time.Duration            // init + fetch + export bound; default 2m

	// CloneDir, if set, is the only repository Cleanup runs git worktree
	// admin in. A handle whose RepoDir differs (stored before #113, when
	// RepoDir was the operator's checkout) has its worktree made writable and
	// removed with os.RemoveAll, with no git call, and one
	// executor.cleanup_foreign_repo Info log. "" = always use h.RepoDir.
	CloneDir string
}

// Local implements executor.Executor on this host.
type Local struct {
	root, dockerBin, pathEnv string
	agentImage               string
	ws                       *workspace.Workspace
	allowEnv, allowFixed     map[string]bool
	configDirEnv             string
	grace, buildTimeout      time.Duration
	now                      func() time.Time
	log                      *slog.Logger
	onHostUntrusted          func(ctx context.Context, h executor.Handle) error

	mu     sync.Mutex
	tracks map[string]*track
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
	if o.AgentImage != "" {
		if err := executor.CheckImageRef(o.AgentImage); err != nil {
			return nil, err
		}
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
	ws, err := workspace.New(workspace.Options{Root: o.Root, Git: o.Git, SidecarGit: o.SidecarGit,
		SidecarURL: o.SidecarURL, SidecarToken: o.SidecarToken, SidecarTimeout: o.SidecarTimeout,
		CloneDir: o.CloneDir, Now: o.Now, Log: o.Log})
	if err != nil {
		return nil, err
	}
	allow := map[string]bool{}
	for _, n := range o.AllowEnv {
		allow[n] = true
	}
	fixed := map[string]bool{}
	for _, n := range o.AllowFixed {
		fixed[n] = true
	}
	return &Local{configDirEnv: o.ConfigDirEnv, allowFixed: fixed, root: o.Root, dockerBin: o.DockerBin, agentImage: o.AgentImage, pathEnv: o.PathEnv, ws: ws,
		allowEnv: allow, grace: o.KillGrace, buildTimeout: o.BuildTimeout, now: o.Now, log: o.Log, onHostUntrusted: o.OnHostUntrusted,
		tracks: map[string]*track{}}, nil
}

// Prepare builds the job's directories through the workspace and, for a
// Dockerfile role, ensures the image. It starts no process.
func (l *Local) Prepare(ctx context.Context, j executor.Job) (h executor.Handle, err error) {
	h, err = l.ws.Prepare(ctx, j)
	if err != nil {
		return h, err
	}
	defer func() {
		if err != nil {
			if derr := l.ws.Discard(context.WithoutCancel(ctx), h); derr != nil {
				err = errors.Join(err, derr)
			}
			h = executor.Handle{}
		}
	}()
	useDocker := j.Dockerfile != ""
	if !useDocker && l.agentImage != "" && !j.TrustedInput {
		useDocker = true
	}
	if useDocker {
		h.Kind = "docker"
		h.Container = "ard-" + j.ID
		if j.Dockerfile != "" {
			// A role's own Dockerfile wins over the default image.
			h.Image, err = l.ensureImage(ctx, j, h.LogPath)
		} else {
			// No fallback to host mode: any failure fails Prepare.
			h.Image = l.agentImage
			err = l.ensurePulled(ctx, l.agentImage, h.LogPath)
		}
		if err != nil {
			return h, err
		}
		h.View = executor.Paths{Work: "/work", Role: "/role", ConfigDir: "/ard-config"}
		if h.HistoryDir != "" {
			h.View.History = "/history"
		}
		if h.SidecarDir != "" {
			h.View.Sidecar = "/sidecar"
		}
	} else if j.ReadOnly {
		// Last: nothing else may need to write into the worktree.
		if err = workspace.ChmodReadOnly(h.Worktree); err != nil {
			return h, fmt.Errorf("make worktree read-only: %w", err)
		}
	}
	return h, nil
}

// WorktreePath is where a job's worktree lives.
func (l *Local) WorktreePath(repo, jobID string) (string, error) {
	return l.ws.WorktreePath(repo, jobID)
}

// LogBase is the log file basename (no extension); see workspace.LogBase.
func LogBase(stage, role string, ts time.Time) string { return workspace.LogBase(stage, role, ts) }

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
	return l.ws.Prune(ctx, olderThan)
}
