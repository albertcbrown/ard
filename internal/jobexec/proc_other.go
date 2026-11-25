//go:build !unix

package jobexec

import (
	"os"
	"os/exec"
)

const noFollow = 0

var sigChld []os.Signal

func setGroup(*exec.Cmd)                {}
func termGroup(pid int)                 { killPid(pid) }
func killGroup(pid int)                 { killPid(pid) }
func signalNumber(*os.ProcessState) int { return 0 }
func reapOrphans(int)                   {}
func reapAll()                          {}

func killPid(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
