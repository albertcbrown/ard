//go:build unix && !linux

package local

// zombie is false outside Linux: there is no /proc to read, and on macOS
// launchd reaps orphaned processes, so kill(pid, 0) alone is accurate there.
func zombie(int) bool { return false }
