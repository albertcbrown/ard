//go:build unix

package jobpre

import "syscall"

func syscallUmask(m int) { syscall.Umask(m) }
