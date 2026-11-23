//go:build unix

package local

import (
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// A child that has exited but is not waited for is a zombie: alive must say
// false on Linux (#154). Elsewhere the kernel keeps the same zombie, but alive
// has no /proc to tell, so the test only checks Linux.
func TestAliveZombie(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("zombie detection reads /proc (Linux only)")
	}
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for !zombie(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("child %d never became a zombie", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if alive(pid) {
		t.Errorf("alive(%d) = true for a zombie", pid)
	}
}

func TestAliveRunning(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if !alive(cmd.Process.Pid) {
		t.Errorf("alive(%d) = false for a running child", cmd.Process.Pid)
	}
	if alive(0) || alive(-1) {
		t.Error("alive accepts a non-positive pid")
	}
}
