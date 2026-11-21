//go:build !unix

package controller

import "errors"

// ErrLeaderHeld means another ard serve holds the leader lock.
var ErrLeaderHeld = errors.New("another ard serve holds the leader lock")

// Leader is a held leader lock.
type Leader struct{}

// LockPath is unsupported on this platform.
func LockPath(home, owner, repo string) string { return "" }

// AcquireLeader is unsupported on this platform.
func AcquireLeader(string) (*Leader, error) {
	return nil, errors.New("leader lock: unsupported platform")
}

// Release is a no-op.
func (l *Leader) Release() error { return nil }
