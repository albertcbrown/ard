//go:build !unix

package jobpre

func syscallUmask(int) {}
