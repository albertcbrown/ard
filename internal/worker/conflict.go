package worker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/reviewfix"
)

const (
	// conflictPathCap and conflictNameCap bound conflict.md: a path list the
	// agent must read is untrusted input too.
	conflictPathCap = 50
	conflictNameCap = 200
)

// mainRef is where the worker fetches the tip of main in RepoDir. It is
// private to the worker: the committer fetches into its own ref and compares.
const mainRef = "refs/ard/worker-main"

// conflictRound is what a conflict round adds to a job.
type conflictRound struct {
	applies    bool            // the newest requested round is a conflict round
	input      *executor.File  // conflict.md; nil when the merge is clean
	mainTip    string          // the fetched tip of main: the only source of MergeParent
	conflicted map[string]bool // paths the controller's own trial merge left conflicted

	// clean: the trial merge had no conflicts, so no agent runs. wt is the
	// scratch worktree holding the uncommitted merge, branchTip the branch
	// commit it was made on, and release must be called to remove it.
	clean     bool
	wt        string
	branchTip string
	release   func()
}

// conflictInput decides whether the issue's newest requested round is a
// conflict round. If it is, it fetches main from the remote itself, merges it
// into a scratch worktree of the branch (hardened, no commit), records the
// conflicted paths and builds conflict.md. A clean merge leaves input nil.
// mainTip comes only from this fetch, never from a role file or conflict.md.
func (l *Loop) conflictInput(ctx context.Context, n int, branch string) (conflictRound, error) {
	d := l.d
	rs, err := reviewfix.Rounds(ctx, d.Store, n)
	if err != nil {
		return conflictRound{}, fmt.Errorf("read review rounds: %w", err)
	}
	if len(rs) == 0 || rs[len(rs)-1].Phase != reviewfix.PhaseRequested || rs[len(rs)-1].Reason != reviewfix.ReasonConflict {
		return conflictRound{}, nil
	}
	r := rs[len(rs)-1]
	cr := conflictRound{applies: true}
	if err := d.Git.FetchRef(ctx, d.RepoDir, d.RemoteURL, "+refs/heads/main:"+mainRef, d.Token); err != nil {
		return cr, fmt.Errorf("fetch main: %w", err)
	}
	if cr.mainTip, err = d.Git.RevParse(ctx, d.RepoDir, mainRef); err != nil {
		return cr, err
	}
	scratch, err := os.MkdirTemp("", "ard-conflict-")
	if err != nil {
		return cr, err
	}
	wt := filepath.Join(scratch, "wt")
	keep := false
	release := func() {
		_ = d.Git.WorktreeRemove(context.WithoutCancel(ctx), d.RepoDir, wt)
		_ = os.RemoveAll(scratch)
	}
	defer func() {
		if !keep {
			release()
		}
	}()
	if cr.branchTip, err = d.Git.RevParse(ctx, d.RepoDir, localRef(branch)); err != nil {
		return cr, err
	}
	if err := d.Git.WorktreeAdd(ctx, d.RepoDir, wt, cr.branchTip); err != nil {
		return cr, fmt.Errorf("scratch worktree: %w", err)
	}
	paths, err := d.Git.MergeNoCommit(ctx, wt, cr.mainTip)
	if err != nil {
		return cr, fmt.Errorf("trial merge: %w", err)
	}
	if len(paths) == 0 {
		// GitHub's view is stale or the conflict is gone: nothing to resolve.
		// The caller merges main itself, with no agent, and releases wt.
		keep = true
		cr.clean, cr.wt, cr.release = true, wt, release
		return cr, nil
	}
	sort.Strings(paths)
	cr.conflicted = map[string]bool{}
	for _, p := range paths {
		cr.conflicted[p] = true
	}
	var b strings.Builder
	b.WriteString(agentjob.UntrustedHeader)
	fmt.Fprintf(&b, "Conflict round %d: the branch conflicts with main. The merge of main into the branch is already in your worktree, "+
		"uncommitted, with conflict markers in the files below. Resolve each file, leave no markers and do not commit.\n\nConflicted paths (%d):\n", r.N, len(paths))
	for i, p := range paths {
		if i == conflictPathCap {
			fmt.Fprintf(&b, "- … %d more\n", len(paths)-i)
			break
		}
		fmt.Fprintf(&b, "- %s\n", quoteName(p))
	}
	if mb, err := d.Git.MergeBase(ctx, d.RepoDir, cr.mainTip, localRef(branch)); err == nil && mb != "" {
		ours, e1 := d.Git.ChangedPaths(ctx, d.RepoDir, mb, localRef(branch))
		theirs, e2 := d.Git.ChangedPaths(ctx, d.RepoDir, mb, cr.mainTip)
		if e1 == nil && e2 == nil {
			fmt.Fprintf(&b, "\nSince the common ancestor the branch changed %d files and main changed %d files.\n", len(ours), len(theirs))
		}
	}
	out := b.String()
	if len(out) > feedbackCap {
		out = strings.ToValidUTF8(out[:feedbackCap], "") + "\n[truncated]\n"
	}
	cr.input = &executor.File{Path: agentjob.InputDir(n) + "/conflict.md", Data: []byte(out)}
	return cr, nil
}

// quoteName renders a path from git as one escaped, bounded token.
func quoteName(p string) string {
	if len(p) > conflictNameCap {
		p = strings.ToValidUTF8(p[:conflictNameCap], "") + "…"
	}
	return strconv.Quote(p)
}

// markersLeft reports the recorded conflicted paths that still hold a conflict
// marker line (at least seven <, = or > characters, or | for diff3). It reads
// them through os.Root, runs no git and never follows a symlink. Advisory: the
// committer's tree rule is the enforcement.
func markersLeft(root *os.Root, conflicted map[string]bool) []string {
	var hit []string
	for p := range conflicted {
		fi, err := root.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > MaxFileSize {
			continue
		}
		f, err := root.Open(p)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
		f.Close()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if hasMarker(line) {
				hit = append(hit, p)
				break
			}
		}
	}
	sort.Strings(hit)
	return hit
}

func hasMarker(line string) bool {
	for _, c := range []string{"<", "=", ">", "|"} {
		if strings.HasPrefix(line, strings.Repeat(c, 7)) {
			return true
		}
	}
	return false
}

func quoteNames(ps []string) string {
	if len(ps) > 5 {
		ps = append(ps[:5:5], "…")
	}
	q := make([]string, len(ps))
	for i, p := range ps {
		q[i] = quoteName(p)
	}
	return strings.Join(q, ", ")
}

// markersLeft opens the agent's worktree as a root and scans the recorded
// conflicted paths.
func (jb *job) markersLeft(h executor.Handle) []string {
	root, err := os.OpenRoot(h.Worktree)
	if err != nil {
		return nil
	}
	defer root.Close()
	return markersLeft(root, jb.conflicted)
}
