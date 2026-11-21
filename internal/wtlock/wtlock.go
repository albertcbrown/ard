// Package wtlock serialises git worktree administration (worktree add,
// remove, prune) per repository within this process. git's worktree
// bookkeeping under <common-dir>/worktrees is not safe against concurrent
// admin commands (#109). Callers hold the lock only for the admin command
// itself; fetch, commit and push never take it.
package wtlock

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
)

var (
	mu    sync.Mutex
	locks = map[string]chan struct{}{}
)

func slot(key string) chan struct{} {
	mu.Lock()
	defer mu.Unlock()
	c, ok := locks[key]
	if !ok {
		c = make(chan struct{}, 1)
		locks[key] = c
	}
	return c
}

// Lock blocks until this process holds the worktree-administration lock for
// the repository whose common git directory is commonDir, or ctx is done.
// commonDir is made absolute and its symlinks resolved, so two spellings of
// one directory share a lock. unlock is idempotent and releases only this
// holder's acquisition.
func Lock(ctx context.Context, commonDir string) (unlock func(), err error) {
	abs, err := filepath.Abs(commonDir)
	if err != nil {
		return nil, fmt.Errorf("wtlock: %s: %w", commonDir, err)
	}
	key, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("wtlock: %s: %w", commonDir, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("wtlock: wait for %s: %w", key, err)
	}
	c := slot(key)
	select {
	case c <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("wtlock: wait for %s: %w", key, ctx.Err())
	}
	var once sync.Once
	return func() { once.Do(func() { <-c }) }, nil
}
