package committer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

// gh006 fakes pushes: the first `moves` protected rejections also move main
// (a lost race), the next `still` rejections leave main alone, and `trans`
// transient failures come first. Afterwards pushes go through.
type gh006 struct {
	Effects
	e      *env
	t      *testing.T
	moves  int
	still  int
	trans  int
	pushes int
}

func (g *gh006) Push(ctx context.Context, op effects.PushOp) error {
	g.pushes++
	if g.trans > 0 {
		g.trans--
		return fmt.Errorf("gitx: push: %w: remote: Internal Server Error", effects.ErrTransient)
	}
	if g.moves > 0 {
		g.moves--
		rival := filepath.Join(g.e.work, fmt.Sprintf("rival%d", g.pushes))
		gittest.Run(g.t, g.e.work, "clone", "-q", "file://"+g.e.bare, rival)
		_ = os.WriteFile(filepath.Join(rival, "other"), []byte(fmt.Sprint(g.pushes)), 0o644)
		gittest.Run(g.t, rival, "add", "-A")
		gittest.Run(g.t, rival, "commit", "-q", "-m", "rival")
		gittest.Run(g.t, rival, "push", "-q", "origin", "HEAD:main")
		return fmt.Errorf("gitx: push: %w", effects.ErrProtected)
	}
	if g.still > 0 {
		g.still--
		return fmt.Errorf("gitx: push: %w", effects.ErrProtected)
	}
	return g.Effects.Push(ctx, op)
}

func TestProtectedRaceRetried(t *testing.T) {
	e := newEnv(t)
	buf := logBuf(e)
	g := &gh006{Effects: e.live(), e: e, t: t, moves: 1}
	res, err := e.main(t, g).Commit(context.Background(), req(12, "issue.md", "mine"))
	if err != nil || res.Attempts != 2 {
		t.Fatalf("res %+v err %v", res, err)
	}
	if got := e.head(t, "main"); got != res.SHA {
		t.Fatalf("main %s, result %s", got, res.SHA)
	}
	if !strings.Contains(buf.String(), "reason=protected_race") || !strings.Contains(buf.String(), "old=") || !strings.Contains(buf.String(), "new=") {
		t.Fatalf("log:\n%s", buf)
	}
}

func TestProtectedNotMovedFailsAtOnce(t *testing.T) {
	e := newEnv(t)
	buf := logBuf(e)
	g := &gh006{Effects: e.live(), e: e, t: t, still: 1000}
	_, err := e.main(t, g).Commit(context.Background(), req(12, "issue.md", "mine"))
	if !errors.Is(err, ErrProtected) || g.pushes != 1 {
		t.Fatalf("err %v pushes %d", err, g.pushes)
	}
	if !strings.Contains(err.Error(), "ruleset") || !strings.Contains(err.Error(), "ard setup --check") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(buf.String(), "reason=protected") || strings.Contains(buf.String(), "committer.retry") {
		t.Fatalf("log:\n%s", buf)
	}
}

func TestProtectedBranchLaneNoRetry(t *testing.T) {
	g := &gh006{still: 1000}
	c := newCodeEnv(t, nil)
	g.Effects = c.env.live()
	c.b = NewBranches(c.cfg, g, c.g)
	t.Cleanup(func() { _ = c.b.Close(context.Background()) })
	r := c.req(CodeFile{Path: "new.go", Src: c.srcFile(t, "new.go", "package x\n")})
	r.Create = true
	// The create path reports a rejected push as an existing branch; push to
	// the branch first, then update it.
	g.still = 0
	if _, err := c.b.CommitCode(context.Background(), brName, r); err != nil {
		t.Fatal(err)
	}
	g.still, g.pushes = 1000, 0
	r2 := c.req(CodeFile{Path: "two.go", Src: c.srcFile(t, "two.go", "package x\n")})
	_, err := c.b.CommitCode(context.Background(), brName, r2)
	if !errors.Is(err, ErrProtected) || g.pushes != 1 {
		t.Fatalf("err %v pushes %d", err, g.pushes)
	}
}

func TestProtectedRaceBudget(t *testing.T) {
	e := newEnv(t)
	g := &gh006{Effects: e.live(), e: e, t: t, moves: 1000}
	_, err := e.main(t, g).Commit(context.Background(), req(12, "issue.md", "mine"))
	if !errors.Is(err, ErrProtected) && !errors.Is(err, effects.ErrProtected) {
		t.Fatalf("err = %v", err)
	}
	if g.pushes != maxPushes {
		t.Fatalf("pushes = %d, want %d", g.pushes, maxPushes)
	}
}

func TestProtectedMixedBudgetsIndependent(t *testing.T) {
	e := newEnv(t)
	g := &gh006{Effects: e.live(), e: e, t: t, trans: maxTransient, moves: maxPushes - 1}
	res, err := e.main(t, g).Commit(context.Background(), req(12, "issue.md", "mine"))
	if err != nil {
		t.Fatalf("res %+v err %v", res, err)
	}
	if got := e.head(t, "main"); got != res.SHA {
		t.Fatalf("main %s, result %s", got, res.SHA)
	}
}
