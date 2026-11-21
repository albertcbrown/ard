package wtlock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func short(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

func mustLock(t *testing.T, d string) func() {
	t.Helper()
	u, err := Lock(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestLockSameDir(t *testing.T) {
	d := t.TempDir()
	unlock := mustLock(t, d)
	if _, err := Lock(short(t), d); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	unlock()
	mustLock(t, d)()
}

func TestLockSpellings(t *testing.T) {
	d := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(d, link); err != nil {
		t.Fatal(err)
	}
	for name, other := range map[string]string{"symlink": link, "dot": d + "/./"} {
		t.Run(name, func(t *testing.T) {
			unlock := mustLock(t, d)
			defer unlock()
			if _, err := Lock(short(t), other); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("err = %v, want DeadlineExceeded", err)
			}
		})
	}
}

func TestLockIndependentDirs(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	defer mustLock(t, a)()
	u, err := Lock(short(t), b)
	if err != nil {
		t.Fatal(err)
	}
	u()
}

func TestLockMissingDir(t *testing.T) {
	_, err := Lock(context.Background(), filepath.Join(t.TempDir(), "nope"))
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want non-context error", err)
	}
}

func TestLockCancelledCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Lock(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
}

func TestUnlockIdempotent(t *testing.T) {
	d := t.TempDir()
	a := mustLock(t, d)
	a()
	b := mustLock(t, d)
	defer b()
	a()
	if _, err := Lock(short(t), d); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}
