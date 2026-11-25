package effects

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/repopath"
)

// ErrGHUnavailable is returned by ApproveIssue when the gh CLI is not on PATH
// or is not logged in. Callers fall back to showing the issue URL.
var ErrGHUnavailable = errors.New("effects: gh is missing or not logged in")

// ghPath finds the gh binary. A variable for tests.
var ghPath = exec.LookPath

// ApproveIssue adds ard:approved through the operator's own gh, so the label
// event carries the operator's identity and the gate checks their permission.
// ard never reads the operator's token, and the GitHub App client is not used.
func (l *Live) ApproveIssue(ctx context.Context, repo string, n int) error {
	if err := bad("ApproveIssue", n); err != nil {
		return err
	}
	if _, _, err := repopath.Parts(repo); err != nil {
		return l.done(ctx, "ApproveIssue", n, err, "repo", repo)
	}
	path, err := ghPath("gh")
	if err != nil {
		return l.done(ctx, "ApproveIssue", n, ErrGHUnavailable, "repo", repo)
	}
	cmd := exec.CommandContext(ctx, path, "issue", "edit", strconv.Itoa(n), "--add-label", labels.Approved, "-R", repo)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		low := strings.ToLower(msg)
		if strings.Contains(low, "gh auth login") || strings.Contains(low, "not logged in") || strings.Contains(low, "authentication") {
			err = ErrGHUnavailable
		} else {
			if len(msg) > 300 {
				msg = msg[:300]
			}
			err = fmt.Errorf("gh issue edit: %w: %s", err, msg)
		}
		return l.done(ctx, "ApproveIssue", n, err, "repo", repo)
	}
	return l.done(ctx, "ApproveIssue", n, nil, "repo", repo)
}

// ApproveIssue records the call and runs nothing.
func (d *DryRun) ApproveIssue(ctx context.Context, repo string, n int) error {
	_, err := d.emit(ctx, "ApproveIssue", repo, map[string]any{"issue": n, "label": labels.Approved}, false, nil)
	return err
}
