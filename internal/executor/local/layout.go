package local

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// repoSlug turns "owner/repo" into "owner-repo", matching the state dir name.
func repoSlug(repo string) string { return strings.ReplaceAll(repo, "/", "-") }

// WorktreePath is where a job's worktree lives.
func (l *Local) WorktreePath(repo, jobID string) string {
	return filepath.Join(l.root, "work", repoSlug(repo), jobID)
}

func (l *Local) homePath(repo, jobID string) string {
	return filepath.Join(l.root, "work", repoSlug(repo), jobID+".home")
}

func (l *Local) logDir(repo string, issue int) string {
	seg := "noissue"
	if issue > 0 {
		seg = strconv.Itoa(issue)
	}
	return filepath.Join(l.root, "logs", repoSlug(repo), seg)
}

// LogBase is the log file basename (no extension), e.g.
// "worker-go-dev-20261121T101500.000Z". The close-out commit uses the same name.
func LogBase(stage, role string, ts time.Time) string {
	return fmt.Sprintf("%s-%s-%s", stage, role, ts.UTC().Format("20060102T150405.000Z"))
}
