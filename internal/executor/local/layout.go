package local

import (
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/albertcbrown/ard/internal/repopath"
)

// repoDir is <root>/<kind>/<owner>/<repo>, lowercased and validated.
func (l *Local) repoDir(kind, repo string) (string, error) {
	return repopath.Join(filepath.Join(l.root, kind), repo)
}

// WorktreePath is where a job's worktree lives.
func (l *Local) WorktreePath(repo, jobID string) (string, error) {
	d, err := l.repoDir("work", repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, jobID), nil
}

func (l *Local) homePath(repo, jobID string) (string, error) {
	d, err := l.repoDir("work", repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, jobID+".home"), nil
}

func (l *Local) logDir(repo string, issue int) (string, error) {
	seg := "noissue"
	if issue > 0 {
		seg = strconv.Itoa(issue)
	}
	d, err := l.repoDir("logs", repo)
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
