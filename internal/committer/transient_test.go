package committer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

// flaky wraps Effects: its first fail pushes report a transient error. With
// landed, the push reaches the remote first (the 500 came after the update).
type flaky struct {
	Effects
	fail, pushes int
	landed       bool
	onFail       func() // called when a transient error is about to be returned
}

func (f *flaky) Push(ctx context.Context, op effects.PushOp) error {
	f.pushes++
	if f.fail == 0 {
		return f.Effects.Push(ctx, op)
	}
	f.fail--
	if f.landed {
		if err := f.Effects.Push(ctx, op); err != nil {
			return err
		}
	}
	if f.onFail != nil {
		f.onFail()
	}
	return fmt.Errorf("gitx: push: %w: remote: Internal Server Error", effects.ErrTransient)
}

func logBuf(e *env) *bytes.Buffer {
	var buf bytes.Buffer
	e.cfg.Log = slog.New(slog.NewTextHandler(&buf, nil))
	return &buf
}

func TestTransientRetry(t *testing.T) {
	e := newEnv(t)
	buf := logBuf(e)
	f := &flaky{Effects: e.live(), fail: 1}
	c := e.main(t, f)
	res, err := c.Commit(context.Background(), req(12, "issue.md", "mine"))
	if err != nil || res.Attempts != 2 {
		t.Fatalf("res %+v err %v", res, err)
	}
	if got := e.head(t, "main"); got != res.SHA {
		t.Fatalf("main %s, result %s", got, res.SHA)
	}
	if n := strings.Count(buf.String(), "committer.retry"); n != 1 || !strings.Contains(buf.String(), "reason=transient") {
		t.Fatalf("log:\n%s", buf)
	}
}

func TestTransientPushLanded(t *testing.T) {
	e := newEnv(t)
	before := e.head(t, "main")
	f := &flaky{Effects: e.live(), fail: 1, landed: true}
	c := e.main(t, f)
	res, err := c.Commit(context.Background(), req(12, "issue.md", "mine"))
	if err != nil || res.NoOp {
		t.Fatalf("res %+v err %v", res, err)
	}
	if got := e.head(t, "main"); got != res.SHA {
		t.Fatalf("main %s, result %s", got, res.SHA)
	}
	if n := strings.TrimSpace(gittest.Run(t, e.bare, "rev-list", "--count", before+"..main")); n != "1" {
		t.Fatalf("%s new commits, want 1", n)
	}
	if f.pushes != 1 {
		t.Fatalf("pushes = %d", f.pushes)
	}
}

func TestTransientExhausted(t *testing.T) {
	e := newEnv(t)
	f := &flaky{Effects: e.live(), fail: 1000}
	c := e.main(t, f)
	_, err := c.Commit(context.Background(), req(12, "issue.md", "mine"))
	if !errors.Is(err, ErrTransient) || !errors.Is(err, effects.ErrTransient) {
		t.Fatalf("err = %v", err)
	}
	if f.pushes != maxTransient+1 {
		t.Fatalf("pushes = %d, want %d", f.pushes, maxTransient+1)
	}
}

func TestTransientIndependentOfRejections(t *testing.T) {
	e := newEnv(t)
	r := &racer{Effects: &flaky{Effects: e.live(), fail: 3}, e: e, t: t, k: 2, path: "other/file"}
	c := e.main(t, r)
	res, err := c.Commit(context.Background(), req(12, "issue.md", "mine"))
	if err != nil {
		t.Fatalf("res %+v err %v", res, err)
	}
	if got := e.head(t, "main"); got != res.SHA {
		t.Fatalf("main %s, result %s", got, res.SHA)
	}
}

func TestTransientCancelledInBackoff(t *testing.T) {
	e := newEnv(t)
	e.cfg.Backoff = func(int) time.Duration { return time.Hour }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &flaky{Effects: e.live(), fail: 1000, onFail: cancel}
	c := e.main(t, f)
	_, err := c.Commit(ctx, req(12, "issue.md", "mine"))
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrTransient) {
		t.Fatalf("err = %v", err)
	}
	if f.pushes != 1 {
		t.Fatalf("pushes = %d, want 1", f.pushes)
	}
}

func TestTransientBackoffJitter(t *testing.T) {
	c := &core{}
	for n := 1; n <= maxTransient; n++ {
		base := time.Second << (n - 1)
		for i := 0; i < 50; i++ {
			d := c.transientBackoff(n)
			if d < base*3/4 || d > base*5/4 {
				t.Fatalf("backoff(%d) = %v outside ±25%% of %v", n, d, base)
			}
		}
	}
}

func TestTransientCodeLane(t *testing.T) {
	for _, landed := range []bool{false, true} {
		t.Run(fmt.Sprint("landed=", landed), func(t *testing.T) {
			f := &flaky{fail: 1, landed: landed}
			c := newCodeEnv(t, nil)
			f.Effects = c.env.live()
			c.b = NewBranches(c.cfg, f, c.g)
			t.Cleanup(func() { _ = c.b.Close(context.Background()) })
			r := c.req(CodeFile{Path: "new.go", Src: c.srcFile(t, "new.go", "package x\n")})
			r.Create = true
			res, err := c.b.CommitCode(context.Background(), brName, r)
			if err != nil {
				t.Fatal(err)
			}
			if got := c.head(t, brName); got != res.SHA {
				t.Fatalf("branch %s, result %s", got, res.SHA)
			}
			if n := strings.TrimSpace(gittest.Run(t, c.bare, "rev-list", "--count", c.base+".."+brName)); n != "1" {
				t.Fatalf("ahead by %s", n)
			}
		})
	}
}

func TestTransientInitLane(t *testing.T) {
	for _, landed := range []bool{false, true} {
		t.Run(fmt.Sprint("landed=", landed), func(t *testing.T) {
			e := newEnv(t)
			i := e.init(t, &flaky{Effects: e.live(), fail: 1, landed: landed})
			base, err := i.Base(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			res, err := i.Propose(context.Background(), base, starterFiles)
			if err != nil {
				t.Fatal(err)
			}
			if got := e.head(t, InitBranch); got != res.SHA {
				t.Fatalf("remote %s, result %s", got, res.SHA)
			}
		})
	}
}
