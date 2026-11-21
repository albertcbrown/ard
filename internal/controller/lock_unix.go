//go:build unix

package controller

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ErrLeaderHeld means another ard serve holds the leader lock.
var ErrLeaderHeld = errors.New("another ard serve holds the leader lock")

// Leader is a held leader lock. The lock dies with the process, so a crashed
// controller never leaves a stale lock behind.
type Leader struct {
	f    *os.File
	path string
}

// LockPath returns <home>/.ard/locks/<owner>-<repo>.lock, lowercased. It is
// the same in dry-run and live mode, so the two exclude each other on one
// repo. The directory must be on a local filesystem: flock is unreliable on
// network filesystems.
func LockPath(home, owner, repo string) string {
	return filepath.Join(home, ".ard", "locks", strings.ToLower(owner)+"-"+strings.ToLower(repo)+".lock")
}

// AcquireLeader takes an exclusive, non-blocking flock on path. When another
// holder has it, the error wraps ErrLeaderHeld and names the holder.
func AcquireLeader(path string) (*Leader, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("leader lock: create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("leader lock: open %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		defer f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s", ErrLeaderHeld, holder(path))
		}
		return nil, fmt.Errorf("leader lock: flock %s: %w", path, err)
	}
	host, _ := os.Hostname()
	if err := f.Truncate(0); err == nil {
		fmt.Fprintf(f, "pid=%d host=%s started=%s\n", os.Getpid(), host, time.Now().UTC().Format(time.RFC3339))
	}
	return &Leader{f: f, path: path}, nil
}

// holder describes the lock holder from the lock file, or says it is unknown
// (for example while the holder is mid-write).
func holder(path string) string {
	b, err := os.ReadFile(path)
	s := strings.TrimSpace(string(b))
	if err != nil || !strings.HasPrefix(s, "pid=") {
		return fmt.Sprintf("holder unknown (%s)", path)
	}
	return fmt.Sprintf("%s (%s)", s, path)
}

// Release drops the lock. The file stays in place.
func (l *Leader) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
