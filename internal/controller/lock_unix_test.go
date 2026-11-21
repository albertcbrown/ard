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

func TestLockPath(t *testing.T) {
	got, err := LockPath("/h", "Own", "ReP")
	if want := "/h/.ard/locks/own/rep.lock"; err != nil || got != want {
		t.Errorf("got %q, %v want %q", got, err, want)
	}
	a, _ := LockPath("/h", "a-b", "c")
	b, _ := LockPath("/h", "a", "b-c")
	if a == b {
		t.Errorf("a-b/c and a/b-c collide at %s", a)
	}
	for _, in := range [][2]string{{"", "r"}, {"o", ""}, {"..", "r"}, {"o", ".."}, {"a/b", "c"}, {"o", "r/x"}} {
		if _, err := LockPath("/h", in[0], in[1]); err == nil {
			t.Errorf("LockPath(%q,%q) accepted", in[0], in[1])
		}
	}
}

func TestLeaderCreatesOwnerDir0700(t *testing.T) {
	tmp := t.TempDir()
	p, err := LockPath(tmp, "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	l, err := AcquireLeader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	fi, err := os.Stat(filepath.Join(tmp, ".ard", "locks", "o"))
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("owner dir: %v, %v", fi, err)
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
