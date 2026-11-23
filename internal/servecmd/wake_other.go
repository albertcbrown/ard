//go:build !unix

package servecmd

import "context"

// wakeSignals is not supported off Unix.
func wakeSignals(context.Context) <-chan struct{} { return nil }
