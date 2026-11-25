//go:build unix && !linux

package jobexec

func reapOrphans(int) {}
func reapAll()        {}
