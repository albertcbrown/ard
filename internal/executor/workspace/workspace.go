package workspace

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/gitx"
)

// Git is the small slice of git the workspace needs for worktrees.
type Git interface {
	RevParse(ctx context.Context, repo, rev string) (string, error)
	// WorktreeAdd creates a detached worktree at dir, at sha.
	WorktreeAdd(ctx context.Context, repo, dir, sha string) error
	WorktreeRemove(ctx context.Context, repo, dir string, force bool) error
	WorktreePrune(ctx context.Context, repo string) error
}

// Options configures a Workspace.
type Options struct {
	Root string // absolute; required
	Git  Git    // required: the worktree runner

	SidecarGit     SidecarGit               // default gitx.New("")
	SidecarURL     func(repo string) string // default https://github.com/<repo>.git
	SidecarToken   gitx.TokenFunc           // nil = no auth header
	SidecarTimeout time.Duration            // init + fetch + export bound; default 2m

	// CloneDir, if set, is the only repository Remove runs git worktree
	// admin in. A handle whose RepoDir differs (stored before #113, when
	// RepoDir was the operator's checkout) has its worktree made writable and
	// removed with os.RemoveAll, with no git call, and one
	// executor.cleanup_foreign_repo Info log. "" = always use h.RepoDir.
	CloneDir string

	Now func() time.Time
	Log *slog.Logger
}

// Workspace builds and removes job directories under Root.
type Workspace struct {
	root           string
	git            Git
	sgit           SidecarGit
	sidecarURL     func(repo string) string
	sidecarToken   gitx.TokenFunc
	sidecarTimeout time.Duration
	cloneDir       string
	now            func() time.Time
	log            *slog.Logger

	mu           sync.Mutex
	sidecarLocks map[string]chan struct{} // per cache dir, 1-buffer slot; guarded by mu
}

// New returns a Workspace with defaults applied.
func New(o Options) (*Workspace, error) {
	if !filepath.IsAbs(o.Root) {
		return nil, fmt.Errorf("root %q is not absolute", o.Root)
	}
	if o.Git == nil {
		return nil, fmt.Errorf("workspace: no git runner")
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
	return &Workspace{root: o.Root, git: o.Git, sgit: o.SidecarGit, sidecarURL: o.SidecarURL,
		sidecarToken: o.SidecarToken, sidecarTimeout: o.SidecarTimeout, cloneDir: cleanDir(o.CloneDir),
		now: o.Now, log: o.Log, sidecarLocks: map[string]chan struct{}{}}, nil
}

// cleanDir is filepath.Clean, but keeps "" as "".
func cleanDir(p string) string {
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}
