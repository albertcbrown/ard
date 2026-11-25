package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/state"
)

// conflictSetup makes issue n's branch and main diverge: each path holds
// "base", the branch changes it to "branch" and main to "main" (or leaves it
// when mainSide is false, so the merge is clean). It returns the issue, the
// branch name and the tip of main on the remote.
func conflictSetup(t *testing.T, h *harness, paths []string, mainSide bool) (n int, branch, mainTip string) {
	t.Helper()
	n = h.issue("Say hello")
	branch = BranchName("engineer", n, "Say hello")
	write := func(content string) {
		for _, p := range paths {
			writeFile(t, h.clone, p, content)
		}
		gittest.Run(t, h.clone, "add", "-A")
		gittest.Run(t, h.clone, "commit", "-q", "-m", content)
	}
	write("base\n")
	gittest.Run(t, h.clone, "push", "-q", "origin", "main")
	gittest.Run(t, h.clone, "checkout", "-q", "-b", "tmp")
	write("branch\n")
	gittest.Run(t, h.clone, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
	gittest.Run(t, h.clone, "update-ref", localRef(branch), "HEAD")
	gittest.Run(t, h.clone, "checkout", "-q", "main")
	if mainSide {
		write("main\n")
	} else {
		gittest.Run(t, h.clone, "commit", "-q", "--allow-empty", "-m", "unrelated")
	}
	gittest.Run(t, h.clone, "push", "-q", "origin", "main")
	mainTip = strings.TrimSpace(gittest.Run(t, h.clone, "rev-parse", "HEAD"))
	return n, branch, mainTip
}

func requestConflictRound(t *testing.T, h *harness, n int) {
	t.Helper()
	pr := h.gh.SeedPR(github.PullRequest{State: github.StateOpen, Author: self, HeadRef: BranchName("engineer", n, "Say hello"), HeadSHA: "x", BaseRef: "main"})
	data, _ := json.Marshal(map[string]any{"round": 1, "phase": "requested", "reason": "conflict", "pr": pr, "head": "h", "worker": "engineer", "roles": []string{"eng-review"}})
	if err := h.store.AppendEvent(context.Background(), state.Event{Kind: state.EventReviewRound, Issue: n, Role: "engineer", Data: data}); err != nil {
		t.Fatal(err)
	}
}

func TestConflictRoundInput(t *testing.T) {
	h := newHarness(t)
	paths := []string{"a\"b\nc.txt"}
	for i := 0; i < conflictPathCap+5; i++ {
		paths = append(paths, fmt.Sprintf("f%03d.txt", i))
	}
	n, branch, mainTip := conflictSetup(t, h, paths, true)
	requestConflictRound(t, h, n)
	cr, err := h.loop.conflictInput(context.Background(), n, branch)
	if err != nil || !cr.applies || cr.input == nil {
		t.Fatalf("cr = %+v, err = %v", cr, err)
	}
	if cr.mainTip != mainTip {
		t.Errorf("mainTip = %q, want the remote's %q", cr.mainTip, mainTip)
	}
	if len(cr.conflicted) != len(paths) || !cr.conflicted[paths[0]] {
		t.Errorf("conflicted = %d paths", len(cr.conflicted))
	}
	body := string(cr.input.Data)
	if cr.input.Path != fmt.Sprintf(".ard-input/%d/conflict.md", n) || !strings.HasPrefix(body, "UNTRUSTED INPUT") {
		t.Errorf("path %q, body:\n%s", cr.input.Path, body)
	}
	if !strings.Contains(body, `"a\"b\nc.txt"`) || strings.Contains(body, "a\"b\nc") {
		t.Errorf("name not escaped:\n%s", body)
	}
	if !strings.Contains(body, "… 6 more") || strings.Contains(body, "f054.txt") {
		t.Errorf("path list not capped:\n%s", body)
	}
	if len(cr.input.Data) > feedbackCap+64 {
		t.Errorf("conflict.md is %d bytes", len(cr.input.Data))
	}
}

func TestConflictRoundCleanMerge(t *testing.T) {
	h := newHarness(t)
	n, branch, _ := conflictSetup(t, h, []string{"a.txt"}, false)
	requestConflictRound(t, h, n)
	cr, err := h.loop.conflictInput(context.Background(), n, branch)
	if err != nil || !cr.applies || cr.input != nil || len(cr.conflicted) != 0 {
		t.Fatalf("cr = %+v, err = %v; want a round with no input", cr, err)
	}
}

func TestConflictRoundNotARound(t *testing.T) {
	h := newHarness(t)
	n, branch, _ := conflictSetup(t, h, []string{"a.txt"}, true)
	cr, err := h.loop.conflictInput(context.Background(), n, branch)
	if err != nil || cr.applies || cr.input != nil {
		t.Fatalf("cr = %+v, err = %v; want no conflict round", cr, err)
	}
}

func TestConflictPreMergeMismatch(t *testing.T) {
	h := newHarness(t)
	_, branch, mainTip := conflictSetup(t, h, []string{"a.txt", "b.txt"}, true)
	wt := func() string {
		dir := filepath.Join(t.TempDir(), "wt")
		gittest.Run(t, h.clone, "worktree", "add", "--detach", dir, localRef(branch))
		return dir
	}
	jb := func(conf ...string) *job {
		m := map[string]bool{}
		for _, p := range conf {
			m[p] = true
		}
		return &job{l: h.loop, mainTip: mainTip, conflicted: m, mergePending: true}
	}
	ctx := context.Background()
	if err := jb("a.txt", "b.txt").preMerge(ctx, executor.Handle{Worktree: wt()}); err != nil {
		t.Fatalf("matching conflicts: %v", err)
	}
	j := jb("a.txt", "b.txt")
	j.mergePending = false
	if err := j.preMerge(ctx, executor.Handle{Worktree: "/nonexistent"}); err != nil {
		t.Errorf("not pending must be a no-op: %v", err)
	}
	if err := jb("a.txt").preMerge(ctx, executor.Handle{Worktree: wt()}); err == nil || !strings.Contains(err.Error(), "trial merge found 1") {
		t.Errorf("count mismatch: %v", err)
	}
	if err := jb("a.txt", "x.txt").preMerge(ctx, executor.Handle{Worktree: wt()}); err == nil || !strings.Contains(err.Error(), "unexpected conflict") {
		t.Errorf("path mismatch: %v", err)
	}
}

func TestConflictRoundResolved(t *testing.T) {
	h := newHarness(t)
	n, _, mainTip := conflictSetup(t, h, []string{"a.txt"}, true)
	requestConflictRound(t, h, n)
	var sawMarkers bool
	h.agentWrites(map[string]string{"a.txt": "resolved\n"}, roleDone, nil)
	run := h.ex.run
	h.ex.run = func(kind string, j executor.Job, hd executor.Handle) int {
		if kind == "agent" {
			b, _ := os.ReadFile(filepath.Join(hd.Worktree, "a.txt"))
			sawMarkers = strings.Contains(string(b), "<<<<<<<")
			if _, err := os.Stat(filepath.Join(hd.Worktree, fmt.Sprintf(".ard-input/%d/conflict.md", n))); err != nil {
				t.Errorf("conflict.md missing: %v", err)
			}
			if _, err := os.Stat(filepath.Join(hd.Worktree, fmt.Sprintf(".ard-input/%d/review-feedback.md", n))); err == nil {
				t.Error("review-feedback.md present in a conflict round")
			}
		}
		return run(kind, j, hd)
	}
	h.code.shas[0] = h.commitFiles(map[string]string{"a.txt": "resolved\n"})
	h.mustTick()
	if !sawMarkers {
		t.Error("the agent did not see conflict markers")
	}
	if h.code.n() != 1 {
		t.Fatalf("commit requests = %d, want 1; issue labels %v", h.code.n(), h.getIssue(n).Labels)
	}
	if got := h.code.reqs[0].MergeParent; got != mainTip {
		t.Errorf("MergeParent = %q, want the fetched main tip %q", got, mainTip)
	}
	if got := h.code.files[0]["a.txt"]; got != "resolved\n" {
		t.Errorf("committed a.txt = %q", got)
	}
}

func TestConflictMarkersLeftNeedsHuman(t *testing.T) {
	h := newHarness(t)
	n, _, _ := conflictSetup(t, h, []string{"a.txt"}, true)
	requestConflictRound(t, h, n)
	// The agent leaves the merge's markers in place.
	h.agentWrites(map[string]string{"a.txt": "<<<<<<< HEAD\nbranch\n=======\nmain\n>>>>>>> x\n"}, roleDone, nil)
	h.mustTick()
	if h.code.n() != 0 {
		t.Errorf("commit requests = %d, want none", h.code.n())
	}
	if fc := h.only(); fc.Opts.Worker == nil || !strings.Contains(fc.Opts.Worker.Hold, "conflict markers left in 1 file") {
		t.Errorf("finish options %+v, want a marker hold", fc.Opts.Worker)
	}
}
