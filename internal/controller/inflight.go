package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"sync"
)

// ErrStopping is returned by Inflight.Go once shutdown has drained.
var ErrStopping = errors.New("controller is stopping")

// Inflight tracks controller-side work that shutdown must wait for, such as
// close-out and job watchers. It is not for agent processes, which keep
// running across a restart.
type Inflight struct {
	log *slog.Logger
	ctx context.Context

	mu     sync.Mutex
	n      uint64
	names  map[uint64]string
	closed bool
	wg     sync.WaitGroup
}

// NewInflight returns an Inflight whose work runs with ctx.
func NewInflight(ctx context.Context, log *slog.Logger) *Inflight {
	return &Inflight{log: log, ctx: ctx, names: map[uint64]string{}}
}

// Go runs fn in a goroutine with the controller's work context and tracks it
// by name. After shutdown has drained it returns ErrStopping and does not run
// fn. A panic in fn is recovered, logged with its stack and counted finished.
func (i *Inflight) Go(name string, fn func(ctx context.Context)) error {
	i.mu.Lock()
	if i.closed {
		i.mu.Unlock()
		return fmt.Errorf("inflight %s: %w", name, ErrStopping)
	}
	i.n++
	id := i.n
	i.names[id] = name
	i.wg.Add(1)
	i.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				i.log.Error("inflight.panic", "name", name, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			}
			i.mu.Lock()
			delete(i.names, id)
			i.mu.Unlock()
			i.wg.Done()
		}()
		fn(i.ctx)
	}()
	return nil
}

// Pending returns the sorted names of work still running.
func (i *Inflight) Pending() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]string, 0, len(i.names))
	for _, n := range i.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// close stops accepting work. Called by the controller after every tick has
// returned, so nothing can register once it is called.
func (i *Inflight) close() {
	i.mu.Lock()
	i.closed = true
	i.mu.Unlock()
}

// Wait returns a channel closed when every tracked task has finished.
func (i *Inflight) Wait() <-chan struct{} {
	ch := make(chan struct{})
	go func() { i.wg.Wait(); close(ch) }()
	return ch
}
