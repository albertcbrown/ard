// Package wtlock serialises git worktree administration (worktree add,
// remove, prune) per repository within this process. git's worktree
// bookkeeping under <common-dir>/worktrees is not safe against concurrent
// admin commands (#109). Callers hold the lock only for the admin command
// itself. Fetches, which read every <common-dir>/worktrees entry, take the
// shared side (RLock) so they never see a worktree half added or removed;
// commit and push take neither.
package wtlock

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
)

// maxReaders bounds concurrent shared holders per repository.
const maxReaders = 64

// rw is one repository's lock: a writer gate plus a read-slot semaphore. A
// reader takes the gate, one slot, then frees the gate. A writer holds the
// gate and takes every slot, so it excludes readers and cannot be starved.
type rw struct {
	gate  chan struct{}
	slots chan struct{}
}

var (
	mu    sync.Mutex
	locks = map[string]*rw{}
)

func slot(key string) *rw {
	mu.Lock()
	defer mu.Unlock()
	l, ok := locks[key]
	if !ok {
		l = &rw{gate: make(chan struct{}, 1), slots: make(chan struct{}, maxReaders)}
		locks[key] = l
	}
	return l
}

func key(ctx context.Context, commonDir string) (string, error) {
	abs, err := filepath.Abs(commonDir)
	if err != nil {
		return "", fmt.Errorf("wtlock: %s: %w", commonDir, err)
	}
	k, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("wtlock: %s: %w", commonDir, err)
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("wtlock: wait for %s: %w", k, err)
	}
	return k, nil
}

func acquire(ctx context.Context, c chan struct{}, k string) error {
	select {
	case c <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wtlock: wait for %s: %w", k, ctx.Err())
	}
}

// Lock blocks until this process holds the worktree-administration lock
// exclusively for the repository whose common git directory is commonDir, or
// ctx is done. commonDir is made absolute and its symlinks resolved, so two
// spellings of one directory share a lock. unlock is idempotent and releases
// only this holder's acquisition.
func Lock(ctx context.Context, commonDir string) (unlock func(), err error) {
	k, err := key(ctx, commonDir)
	if err != nil {
		return nil, err
	}
	l := slot(k)
	if err := acquire(ctx, l.gate, k); err != nil {
		return nil, err
	}
	for n := 0; n < maxReaders; n++ {
		if err := acquire(ctx, l.slots, k); err != nil {
			for ; n > 0; n-- {
				<-l.slots
			}
			<-l.gate
			return nil, err
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for n := 0; n < maxReaders; n++ {
				<-l.slots
			}
			<-l.gate
		})
	}, nil
}

// RLock is the shared form of Lock: any number of readers hold it together,
// none while a Lock holder or waiting writer has it. Same key, ctx and
// unlock semantics as Lock.
func RLock(ctx context.Context, commonDir string) (unlock func(), err error) {
	k, err := key(ctx, commonDir)
	if err != nil {
		return nil, err
	}
	l := slot(k)
	if err := acquire(ctx, l.gate, k); err != nil {
		return nil, err
	}
	err = acquire(ctx, l.slots, k)
	<-l.gate
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { <-l.slots }) }, nil
}
