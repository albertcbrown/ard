//go:build unix

package servecmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// wakeSignals delivers one value per SIGUSR1 until ctx is done.
func wakeSignals(ctx context.Context) <-chan struct{} {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGUSR1)
	out := make(chan struct{}, 1)
	go func() {
		defer signal.Stop(sig)
		for {
			select {
			case <-ctx.Done():
				return
			case <-sig:
				select {
				case out <- struct{}{}:
				default:
				}
			}
		}
	}()
	return out
}
