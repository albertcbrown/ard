//go:build linux

package local

import (
	"fmt"
	"os"
	"strings"
)

// zombie reports whether pid is a zombie or dead process, from the state field
// of /proc/<pid>/stat. The command name in the second field is in parentheses
// and may itself contain spaces or ')', so the state is the first field after
// the last ')'. An unreadable file means no such process, which alive's kill
// already settled, so it reports false.
func zombie(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	return zombieStat(string(b))
}

// zombieStat parses the contents of a /proc/<pid>/stat file.
func zombieStat(stat string) bool {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return false
	}
	f := strings.Fields(stat[i+1:])
	return len(f) > 0 && (f[0] == "Z" || f[0] == "X")
}
