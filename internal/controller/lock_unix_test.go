//go:build unix

package controller

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if p := os.Getenv("ARD_LOCK_HELPER"); p != "" {
		if _, err := AcquireLeader(p); err != nil {
			os.Stdout.WriteString("err " + err.Error() + "\n")
			os.Exit(1)
		}
		os.Stdout.WriteString("ok\n")
		select {} // hold the lock until killed
	}
	os.Exit(m.Run())
}

func TestLockPathLowercases(t *testing.T) {
	if got, want := LockPath("/h", "Own", "ReP"), "/h/.ard/locks/own-rep.lock"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestLeaderExcludesSecondAndReleases(t *testing.T) {
	p := filepath.Join(t.TempDir(), "locks", "x.lock")
	l, err := AcquireLeader(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = AcquireLeader(p)
	if !errors.Is(err, ErrLeaderHeld) || !strings.Contains(err.Error(), "pid="+strconv.Itoa(os.Getpid())) ||
		!strings.Contains(err.Error(), p) {
		t.Fatalf("err = %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := AcquireLeader(p)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	l2.Release()
}

func TestLockHolderUnknown(t *testing.T) {
	for _, content := range []string{"", "garbage"} {
		p := filepath.Join(t.TempDir(), "x.lock")
		l, err := AcquireLeader(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = AcquireLeader(p)
		if !errors.Is(err, ErrLeaderHeld) || !strings.Contains(err.Error(), "holder unknown") {
			t.Errorf("content %q: err = %v", content, err)
		}
		l.Release()
	}
}

func TestLockDiesWithProcess(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "ARD_LOCK_HELPER="+p)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(out).ReadString('\n')
	if strings.TrimSpace(line) != "ok" {
		t.Fatalf("child said %q", line)
	}
	if _, err := AcquireLeader(p); !errors.Is(err, ErrLeaderHeld) {
		t.Fatalf("while child lives: %v", err)
	}
	cmd.Process.Kill()
	cmd.Wait()
	l, err := AcquireLeader(p)
	if err != nil {
		t.Fatalf("after child died: %v", err)
	}
	l.Release()
}
