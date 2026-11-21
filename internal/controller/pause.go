package controller

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/repopath"
)

// Reason says why the controller is paused.
type Reason string

// Pause reasons.
const (
	ReasonNone          Reason = ""
	ReasonConfig        Reason = "config"         // cfg.Paused (paused: true on origin/main)
	ReasonConfigInvalid Reason = "config-invalid" // reload failed (set by the scheduler)
	ReasonLabel         Reason = "label"          // ard:pause on the status issue
	ReasonUnreadable    Reason = "status-unreadable"
	// ReasonPauseFile: something exists at the local pause file path.
	ReasonPauseFile Reason = "pause-file"
	// ReasonPauseFileUnreadable: the pause file path cannot be examined.
	ReasonPauseFileUnreadable Reason = "pause-file-unreadable"
	// ReasonLocalConfig: the host's working-tree config says paused: true.
	ReasonLocalConfig Reason = "local-config"
)

// PauseState is the result of a kill-switch check.
type PauseState struct {
	Paused bool
	Reason Reason
	Err    error // set for ReasonUnreadable and ReasonPauseFileUnreadable
}

// Pauser decides whether claiming loops may tick.
type Pauser interface {
	Check(ctx context.Context, cfg *config.Resolved) PauseState
}

// IssueGetter is the one GitHub read the gate needs.
type IssueGetter interface {
	GetIssue(ctx context.Context, n int) (github.Issue, error)
}

// PauseGate checks the four kill-switch sources, cheapest first: the local
// pause file, the host working tree's `paused: true`, `paused: true` in the
// config loaded from origin/main, and the status issue's ard:pause label. It
// fails closed: an unreadable pause file path or status issue counts as
// paused. It is safe for concurrent use.
type PauseGate struct {
	gh  IssueGetter
	log *slog.Logger

	mu          sync.Mutex
	issue       int
	pauseFile   string // "" = no pause file check
	localRoot   string // "" = no local working-tree check
	unreadable  bool   // status issue: last check failed; for once-per-transition logging
	fileErr     bool   // pause file: last check failed
	localErr    bool   // local config: last read failed
	warnedClose bool
}

// PauseFilePath returns <home>/.ard/pause/<owner>/<repo>, lowercased (see
// repopath). Live and dry-run share it, like the leader lock.
func PauseFilePath(home, owner, repo string) (string, error) {
	p, err := repopath.Join(filepath.Join(home, ".ard", "pause"), owner+"/"+repo)
	if err != nil {
		return "", fmt.Errorf("pause file: %w", err)
	}
	return p, nil
}

// SetPauseFile sets the local pause file; "" disables the check.
func (g *PauseGate) SetPauseFile(path string) {
	g.mu.Lock()
	g.pauseFile = path
	g.fileErr = false
	g.mu.Unlock()
}

// SetLocalConfig names the working tree whose .ard/config.yaml is read for
// `paused: true` and nothing else; "" disables the check. A file that is
// missing, unreadable or invalid does not pause by itself.
func (g *PauseGate) SetLocalConfig(root string) {
	g.mu.Lock()
	g.localRoot = root
	g.localErr = false
	g.mu.Unlock()
}

// NewPauseGate builds a gate with no status issue; until SetStatusIssue is
// called with a positive number only config pauses.
func NewPauseGate(gh IssueGetter, log *slog.Logger) *PauseGate {
	if log == nil {
		log = slog.Default()
	}
	return &PauseGate{gh: gh, log: log}
}

// SetStatusIssue sets the status issue number. n <= 0 (unset, or a dry-run
// synthetic number) means config only.
func (g *PauseGate) SetStatusIssue(n int) {
	g.mu.Lock()
	g.issue = n
	g.unreadable, g.warnedClose = false, false
	g.mu.Unlock()
}

// Check implements Pauser. It makes no cache: one GetIssue per call.
func (g *PauseGate) Check(ctx context.Context, cfg *config.Resolved) PauseState {
	if st := g.checkLocal(); st.Paused {
		return st
	}
	if cfg != nil && cfg.Paused {
		return PauseState{Paused: true, Reason: ReasonConfig}
	}
	g.mu.Lock()
	n := g.issue
	g.mu.Unlock()
	if n <= 0 {
		return PauseState{}
	}
	is, err := g.gh.GetIssue(ctx, n)
	if err != nil {
		g.mu.Lock()
		first := !g.unreadable
		g.unreadable = true
		g.mu.Unlock()
		if first {
			if errors.Is(err, github.ErrNotFound) {
				g.log.Warn("pause.status_issue_unreadable", "issue", n, "error", err,
					"hint", "status issue is gone; restart ard serve to recreate it")
			} else {
				g.log.Warn("pause.status_issue_unreadable", "issue", n, "error", err)
			}
		}
		return PauseState{Paused: true, Reason: ReasonUnreadable, Err: err}
	}
	g.mu.Lock()
	if g.unreadable {
		g.log.Info("pause.status_issue_readable", "issue", n)
	}
	g.unreadable = false
	warn := is.State == github.StateClosed && !g.warnedClose
	if warn {
		g.warnedClose = true
	}
	g.mu.Unlock()
	if warn {
		g.log.Warn("pause.status_issue_closed", "issue", n)
	}
	if slices.Contains(is.Labels, labels.Pause) {
		return PauseState{Paused: true, Reason: ReasonLabel}
	}
	return PauseState{}
}

// checkLocal checks the two sources on the host: the pause file, then the
// working tree's config. Nothing remote can lift either.
func (g *PauseGate) checkLocal() PauseState {
	g.mu.Lock()
	path, root := g.pauseFile, g.localRoot
	g.mu.Unlock()
	if path != "" {
		// Presence is the signal, whatever is there: file, directory or symlink.
		_, err := os.Lstat(path)
		switch {
		case err == nil:
			return PauseState{Paused: true, Reason: ReasonPauseFile}
		case errors.Is(err, fs.ErrNotExist):
			g.setFlag(&g.fileErr, false, func() { g.log.Info("pause.file_readable", "path", path) })
		default:
			g.setFlag(&g.fileErr, true, func() { g.log.Warn("pause.file_unreadable", "path", path, "error", err) })
			return PauseState{Paused: true, Reason: ReasonPauseFileUnreadable, Err: err}
		}
	}
	if root != "" {
		paused, err := config.LocalPaused(os.DirFS(root))
		if err != nil {
			g.setFlag(&g.localErr, true, func() { g.log.Warn("pause.local_config_unreadable", "root", root, "error", err) })
		} else {
			g.setFlag(&g.localErr, false, nil)
		}
		if paused {
			return PauseState{Paused: true, Reason: ReasonLocalConfig}
		}
	}
	return PauseState{}
}

// setFlag sets *flag to v under the lock and runs onChange, outside it, when
// the value changed.
func (g *PauseGate) setFlag(flag *bool, v bool, onChange func()) {
	g.mu.Lock()
	changed := *flag != v
	*flag = v
	g.mu.Unlock()
	if changed && onChange != nil {
		onChange()
	}
}
