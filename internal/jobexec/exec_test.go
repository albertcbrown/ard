//go:build unix

package jobexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/jobpod"
)

// TestMain turns the test binary into the fake agent when ARD_FAKE_AGENT is set.
func TestMain(m *testing.M) {
	if mode := os.Getenv("ARD_FAKE_AGENT"); mode != "" {
		fakeAgent(mode)
		return
	}
	os.Exit(m.Run())
}

func fakeAgent(mode string) {
	switch {
	case strings.HasPrefix(mode, "exit:"):
		n, _ := strconv.Atoi(strings.TrimPrefix(mode, "exit:"))
		fmt.Fprintln(os.Stdout, "to-stdout")
		fmt.Fprintln(os.Stderr, "to-stderr")
		os.Exit(n)
	case mode == "sleep":
		time.Sleep(time.Minute)
	case mode == "ignoreterm":
		ignoreTerm()
		fmt.Println("ready")
		time.Sleep(time.Minute)
	case mode == "grandchild":
		c := exec.Command("sleep", "60")
		c.Start()
		os.WriteFile(os.Getenv("ARD_PIDFILE"), []byte(strconv.Itoa(c.Process.Pid)), 0o644)
	case mode == "perm":
		os.WriteFile(filepath.Join(os.Getenv("ARD_WORK"), "secret.txt"), []byte("x"), 0o600)
		os.Mkdir(filepath.Join(os.Getenv("ARD_WORK"), "d"), 0o700)
		os.WriteFile(filepath.Join(os.Getenv("ARD_WORK"), "d", "f"), []byte("x"), 0o600)
	}
	os.Exit(0)
}

func ignoreTerm() { signalIgnore(syscall.SIGTERM) }

type env struct {
	work, out, home string
	stdout, stderr  *bytes.Buffer
}

func setup(t *testing.T, mode string) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{work: filepath.Join(root, "work"), out: filepath.Join(root, "out"), home: filepath.Join(root, "job", "home"),
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	for _, d := range []string{e.work, e.out, filepath.Dir(e.home)} {
		os.MkdirAll(d, 0o755)
	}
	homeDir, workDir, outDir, stdout, stderr = e.home, e.work, e.out, e.stdout, e.stderr
	t.Cleanup(func() {
		homeDir, workDir, outDir = jobpod.JobHome, jobpod.MountWork, jobpod.MountOut
		stdout, stderr = os.Stdout, os.Stderr
	})
	t.Setenv("ARD_FAKE_AGENT", mode)
	t.Setenv("ARD_WORK", e.work)
	return e
}

func (e *env) run(ctx context.Context, d time.Duration, grace time.Duration) int {
	o := cli.JobExecOpts{Grace: grace, Argv: []string{os.Args[0]}}
	if d > 0 {
		o.Deadline = time.Now().Add(d)
	}
	err := Run(ctx, &cli.Env{}, o)
	var ee *cli.ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	if err != nil {
		return -1
	}
	return 0
}

func (e *env) done(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.out, jobpod.DoneName))
	if err != nil {
		t.Fatalf("no done marker: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func TestExecExitCodes(t *testing.T) {
	for _, n := range []int{0, 3} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			e := setup(t, "exit:"+strconv.Itoa(n))
			if got := e.run(context.Background(), time.Minute, time.Second); got != n {
				t.Fatalf("exit = %d, want %d", got, n)
			}
			if e.done(t) != strconv.Itoa(n) {
				t.Errorf("marker = %q", e.done(t))
			}
			file, _ := os.ReadFile(filepath.Join(e.out, jobpod.StdoutName))
			if string(file) != "to-stdout\n" || e.stdout.String() != "to-stdout\n" {
				t.Errorf("stdout file %q, stream %q", file, e.stdout.String())
			}
			if e.stderr.String() != "to-stderr\n" || strings.Contains(string(file), "stderr") {
				t.Errorf("stderr %q", e.stderr.String())
			}
		})
	}
}

func TestExecHome(t *testing.T) {
	e := setup(t, "exit:0")
	e.run(context.Background(), time.Minute, time.Second)
	for _, d := range []string{e.home, filepath.Join(e.home, ".claude")} {
		st, err := os.Stat(d)
		if err != nil || st.Mode().Perm() != 0o700 && d != e.home {
			t.Errorf("%s: %v %v", d, st, err)
		}
	}
}

func TestExecDeadline(t *testing.T) {
	e := setup(t, "sleep")
	start := time.Now()
	if got := e.run(context.Background(), 300*time.Millisecond, time.Second); got != ExitDeadline {
		t.Fatalf("exit = %d, want %d", got, ExitDeadline)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("deadline not enforced promptly")
	}
	if e.done(t) != "124" {
		t.Errorf("marker = %q", e.done(t))
	}
}

func TestExecKillAfterGrace(t *testing.T) {
	e := setup(t, "ignoreterm")
	start := time.Now()
	if got := e.run(context.Background(), 500*time.Millisecond, 500*time.Millisecond); got != ExitDeadline {
		t.Fatalf("exit = %d", got)
	}
	if el := time.Since(start); el < 900*time.Millisecond || el > 10*time.Second {
		t.Errorf("elapsed %v: want SIGTERM at 0.5s then SIGKILL after the grace", el)
	}
}

func TestExecForwardedSIGTERM(t *testing.T) {
	e := setup(t, "sleep")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	if got := e.run(ctx, time.Minute, time.Second); got != 128+15 {
		t.Fatalf("exit = %d, want 143", got)
	}
	e.done(t)
}

func TestExecKillsGrandchild(t *testing.T) {
	e := setup(t, "grandchild")
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("ARD_PIDFILE", pidFile)
	if got := e.run(context.Background(), time.Minute, time.Second); got != 0 {
		t.Fatalf("exit = %d", got)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(b))
	var alive bool
	for i := 0; i < 50; i++ {
		// A killed grandchild is a zombie until its parent reaps it; signal 0
		// to a zombie still succeeds, so check the process state.
		out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		s := strings.TrimSpace(string(out))
		alive = s != "" && !strings.HasPrefix(s, "Z")
		if !alive {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if alive {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Error("grandchild survived")
	}
}

func TestExecPlantedDoneAndPerms(t *testing.T) {
	e := setup(t, "perm")
	os.WriteFile(filepath.Join(e.out, jobpod.DoneName), []byte("planted-planted-planted"), 0o644)
	if got := e.run(context.Background(), time.Minute, time.Second); got != 0 {
		t.Fatalf("exit = %d", got)
	}
	if e.done(t) != "0" {
		t.Errorf("marker = %q", e.done(t))
	}
	if st, _ := os.Stat(filepath.Join(e.work, "secret.txt")); st.Mode().Perm() != 0o640 {
		t.Errorf("file mode %v", st.Mode().Perm())
	}
	if st, _ := os.Stat(filepath.Join(e.work, "d")); st.Mode().Perm() != 0o750 {
		t.Errorf("dir mode %v", st.Mode().Perm())
	}
}

func TestExecMissingCommand(t *testing.T) {
	e := setup(t, "")
	err := Run(context.Background(), &cli.Env{}, cli.JobExecOpts{Argv: []string{"/nonexistent/agent"}, Grace: time.Second})
	var ee *cli.ExitError
	if !errors.As(err, &ee) || ee.Code != 127 {
		t.Fatalf("err = %v", err)
	}
	if e.done(t) != "127" {
		t.Error("no marker for a command that did not start")
	}
}

func TestExecNoArgv(t *testing.T) {
	setup(t, "")
	err := Run(context.Background(), &cli.Env{}, cli.JobExecOpts{})
	var ee *cli.ExitError
	if !errors.As(err, &ee) || ee.Code != 2 {
		t.Fatalf("err = %v", err)
	}
}
