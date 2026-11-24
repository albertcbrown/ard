package workspace

import (
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/albertcbrown/ard/internal/repopath"
)

// repoDir is <root>/<kind>/<owner>/<repo>, lowercased and validated.
func (w *Workspace) repoDir(kind, repo string) (string, error) {
	return repopath.Join(filepath.Join(w.root, kind), repo)
}

// WorktreePath is where a job's worktree lives.
func (w *Workspace) WorktreePath(repo, jobID string) (string, error) {
	d, err := w.repoDir("work", repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, jobID), nil
}

// HomePath is where a job's home lives.
func (w *Workspace) HomePath(repo, jobID string) (string, error) {
	d, err := w.repoDir("work", repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, jobID+".home"), nil
}

// LogDir is the directory holding a job's log files.
func (w *Workspace) LogDir(repo string, issue int) (string, error) {
	seg := "noissue"
	if issue > 0 {
		seg = strconv.Itoa(issue)
	}
	d, err := w.repoDir("logs", repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, seg), nil
}

// LogBase is the log file basename (no extension), e.g.
// "worker-go-dev-20261121T101500.000Z". The close-out commit uses the same name.
func LogBase(stage, role string, ts time.Time) string {
	return fmt.Sprintf("%s-%s-%s", stage, role, ts.UTC().Format("20060102T150405.000Z"))
}
