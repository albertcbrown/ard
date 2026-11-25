package jobexec

import (
	"encoding/binary"
	"os"
	"syscall"
	"unsafe"
)

const (
	pAll    = 0 // P_ALL
	wNoWait = 0x01000000
)

// peekChild returns the pid of an exited child without reaping it, or 0.
func peekChild() int {
	var info [128]byte // siginfo_t
	_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, pAll, 0, uintptr(unsafe.Pointer(&info[0])),
		syscall.WEXITED|syscall.WNOHANG|wNoWait, 0, 0)
	if errno != 0 || binary.NativeEndian.Uint32(info[0:4]) == 0 { // si_signo
		return 0
	}
	off := 16 // si_pid follows three ints and, on 64-bit, 4 bytes of padding
	if unsafe.Sizeof(uintptr(0)) == 4 {
		off = 12
	}
	return int(int32(binary.NativeEndian.Uint32(info[off : off+4])))
}

// reapOrphans collects zombie children that are not the agent, which os/exec
// is still going to wait for. When job-exec is PID 1 of the container, the
// agent's orphaned grandchildren are re-parented to it. It peeks with
// WNOWAIT so the agent's own status is never taken.
func reapOrphans(agent int) {
	if os.Getpid() != 1 {
		return // only PID 1 inherits orphans; elsewhere wait4(-1) could take a stranger's child
	}
	for {
		pid := peekChild()
		if pid <= 0 || pid == agent {
			return
		}
		var ws syscall.WaitStatus
		if _, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil); err != nil {
			return
		}
	}
}

// reapAll collects every exited child; the agent has been waited for.
func reapAll() {
	if os.Getpid() != 1 {
		return
	}
	for {
		var ws syscall.WaitStatus
		if pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil); err != nil || pid <= 0 {
			return
		}
	}
}
