package worker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const maxSlug = 40

// Slug turns an issue title into a branch-name part: lowercase ASCII letters
// and digits, every other run of characters one "-", trimmed at both ends, at
// most 40 characters. An empty result is "issue".
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		} else {
			dash = true
		}
	}
	s := b.String()
	if len(s) > maxSlug {
		s = strings.TrimRight(s[:maxSlug], "-")
	}
	if s == "" {
		return "issue"
	}
	return s
}

// branchPrefix is the start of every branch name of one role and issue.
func branchPrefix(role string, n int) string { return fmt.Sprintf("worker/%s-%d-", role, n) }

// BranchName is worker/<role>-<issue#>-<slug>. Config validation keeps role
// names free of all-digit segments, so the prefix names one role and issue.
func BranchName(role string, n int, title string) string {
	return branchPrefix(role, n) + Slug(title)
}

var errAmbiguousBranch = errors.New("more than one worker branch for this issue")

// resolveBranch looks for the branch of (role, issue) on the remote: the title
// may have changed since the first round, so the slug is not trusted. One
// match is fetched to refs/ard/worker/<branch> in RepoDir and returned with
// exists true; no match gives the name a new branch would get.
func (l *Loop) resolveBranch(ctx context.Context, role string, n int, title string) (branch string, exists bool, err error) {
	d := l.d
	prefix := branchPrefix(role, n)
	refs, err := d.Git.LsRemote(ctx, d.RepoDir, d.RemoteURL, "refs/heads/"+prefix+"*", d.Token)
	if err != nil {
		return "", false, fmt.Errorf("list branches of issue %d: %w", n, err)
	}
	var found []string
	for ref := range refs {
		if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok && strings.HasPrefix(name, prefix) {
			found = append(found, name)
		}
	}
	sort.Strings(found)
	switch len(found) {
	case 0:
		return BranchName(role, n, title), false, nil
	case 1:
		if err := d.Git.FetchRef(ctx, d.RepoDir, d.RemoteURL, fetchSpec(found[0]), d.Token); err != nil {
			return "", false, fmt.Errorf("fetch branch %s: %w", found[0], err)
		}
		return found[0], true, nil
	}
	return "", false, fmt.Errorf("issue %d: %w", n, errAmbiguousBranch)
}

func fetchSpec(branch string) string { return "+refs/heads/" + branch + ":" + localRef(branch) }

// localRef is where an existing branch is fetched to in RepoDir. The executor
// resolves it as the job's Ref.
func localRef(branch string) string { return "refs/ard/" + branch }
