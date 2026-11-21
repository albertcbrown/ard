package controller

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
)

// logBuf is a goroutine-safe slog destination.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (l *logBuf) count(sub string) int { return strings.Count(l.String(), sub) }

func newLog() (*slog.Logger, *logBuf) {
	b := &logBuf{}
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug})), b
}

// testLoop is a configurable Loop recording when it ticked.
type testLoop struct {
	name     string
	claims   bool
	interval func(*config.Resolved) time.Duration
	tick     func(ctx context.Context, env Env) error

	mu     sync.Mutex
	starts []time.Time
	ends   []time.Time
	cur    int
	maxCur int
}

func (l *testLoop) Name() string { return l.name }
func (l *testLoop) Claims() bool { return l.claims }
func (l *testLoop) Interval(c *config.Resolved) time.Duration {
	if l.interval != nil {
		return l.interval(c)
	}
	return 5 * time.Minute
}

func (l *testLoop) Tick(ctx context.Context, env Env) error {
	l.mu.Lock()
	l.starts = append(l.starts, time.Now())
	l.cur++
	l.maxCur = max(l.maxCur, l.cur)
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.cur--
		l.ends = append(l.ends, time.Now())
		l.mu.Unlock()
	}()
	if l.tick != nil {
		return l.tick(ctx, env)
	}
	return nil
}

func (l *testLoop) ticks() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.starts)
}

// gaps returns the time from each tick's end to the next tick's start.
func (l *testLoop) gaps() []time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []time.Duration
	for i := 1; i < len(l.starts) && i <= len(l.ends); i++ {
		out = append(out, l.starts[i].Sub(l.ends[i-1]))
	}
	return out
}

type pauseFunc func() PauseState

func (f pauseFunc) Check(context.Context, *config.Resolved) PauseState { return f() }

var never = pauseFunc(func() PauseState { return PauseState{} })

func staticCfg(context.Context) (*config.Resolved, error) { return &config.Resolved{}, nil }

func fixedRand(r float64) func() float64 { return func() float64 { return r } }

// start runs c in a goroutine and returns a stop func that cancels and
// returns Run's result.
func start(t *testing.T, c *Controller) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { res <- c.Run(ctx) }()
	return func() error { cancel(); return <-res }
}

func mustNew(t *testing.T, d Deps, loops ...Loop) *Controller {
	t.Helper()
	if d.Config == nil {
		d.Config = staticCfg
	}
	if d.Pause == nil {
		d.Pause = never
	}
	c, err := New(d, loops...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
