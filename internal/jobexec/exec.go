// Package jobexec is `ard job-exec`, the agent step of a Kubernetes job pod.
// It runs the agent command as a child in its own process group, tees stdout
// to a file for the post container, enforces the deadline and writes the done
// marker. It reads no credential and no Secret path.
package jobexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"time"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/executor/workspace"
	"github.com/albertcbrown/ard/internal/jobpod"
)

// ExitDeadline is the exit code when the deadline killed the agent.
const ExitDeadline = 124

// Redirectable for tests.
var (
	homeDir           = jobpod.JobHome
	workDir           = jobpod.MountWork
	outDir            = jobpod.MountOut
	stdout  io.Writer = os.Stdout
	stderr  io.Writer = os.Stderr
)

// Run runs the agent command and returns its exit status as a cli.ExitError.
func Run(ctx context.Context, e *cli.Env, o cli.JobExecOpts) error {
	code, err := run(ctx, o)
	if err != nil {
		return err
	}
	if code != 0 {
		return &cli.ExitError{Code: code, Err: fmt.Errorf("agent exited with status %d", code)}
	}
	return nil
}

func run(ctx context.Context, o cli.JobExecOpts) (int, error) {
	if len(o.Argv) == 0 {
		return 0, &cli.ExitError{Code: 2, Err: errors.New("job-exec: no command")}
	}
	if _, err := workspace.MakeHome(homeDir); err != nil {
		return 0, err
	}
	outFile, err := os.OpenFile(filepath.Join(outDir, jobpod.StdoutName),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC|noFollow, 0o644)
	if err != nil {
		return 0, err
	}
	defer outFile.Close()
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return 0, err
	}
	defer devNull.Close()

	cmd := exec.Command(o.Argv[0], o.Argv[1:]...)
	setGroup(cmd)
	cmd.Stdin = devNull
	cmd.Stdout = io.MultiWriter(stdout, outFile)
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second

	chld := make(chan os.Signal, 8)
	signal.Notify(chld, sigChld...)
	defer signal.Stop(chld)

	code := 0
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "job-exec: %v\n", err)
		code = 127
		if errors.Is(err, fs.ErrPermission) {
			code = 126
		}
		return code, finish(outFile, code)
	}
	pid := cmd.Process.Pid
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	var deadline <-chan time.Time
	if !o.Deadline.IsZero() {
		t := time.NewTimer(time.Until(o.Deadline))
		defer t.Stop()
		deadline = t.C
	}
	var kill <-chan time.Time
	ctxDone := ctx.Done()
	timedOut := false
	terminate := func() {
		if kill == nil {
			termGroup(pid)
			t := time.NewTimer(o.Grace)
			// Stopped on return; the timer is only read in this function.
			kill = t.C
		}
	}
loop:
	for {
		select {
		case werr := <-waitCh:
			code = exitCode(cmd, werr)
			break loop
		case <-chld:
			reapOrphans(pid)
		case <-deadline:
			deadline = nil
			timedOut = true
			terminate()
		case <-ctxDone:
			ctxDone = nil
			terminate()
		case <-kill:
			killGroup(pid)
			kill = make(chan time.Time) // never fires again
		}
	}
	killGroup(pid)
	reapAll()
	if timedOut {
		code = ExitDeadline
	}
	return code, finish(outFile, code)
}

func exitCode(cmd *exec.Cmd, werr error) int {
	if ps := cmd.ProcessState; ps != nil {
		if c := ps.ExitCode(); c >= 0 {
			return c
		}
		return 128 + signalNumber(ps)
	}
	if werr != nil {
		return 1
	}
	return 0
}

// finish makes the work tree group-readable, syncs stdout and writes the done
// marker. The marker comes last: post serves nothing before it exists.
func finish(outFile *os.File, code int) error {
	groupReadable(workDir)
	if err := outFile.Sync(); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(outDir, jobpod.DoneName),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC|noFollow, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(strconv.Itoa(code) + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// groupReadable adds g+r to files and g+rx to directories under root, so the
// post container, another user in the same group, can read what the agent
// wrote. Symlinks are never followed.
func groupReadable(root string) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return nil
		}
		switch {
		case d.IsDir():
			os.Chmod(p, st.Mode().Perm()|0o050)
		case st.Mode().IsRegular():
			os.Chmod(p, st.Mode().Perm()|0o040)
		}
		return nil
	})
}
