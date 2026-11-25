//go:build unix

package jobexec

import (
	"os"
	"os/exec"
	"syscall"
)

const noFollow = syscall.O_NOFOLLOW

var sigChld = []os.Signal{syscall.SIGCHLD}

func setGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func termGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGTERM) }
func killGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }

func signalNumber(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return int(ws.Signal())
	}
	return 0
}
