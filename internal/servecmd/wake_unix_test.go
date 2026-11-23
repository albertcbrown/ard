//go:build unix

package servecmd

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestWakeSignals(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := wakeSignals(ctx)
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no wake after SIGUSR1")
	}
	cancel()
}
