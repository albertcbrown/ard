package kube

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
)

// doneFile is the controller's <id>.done marker: the job has ended and, if End
// is empty, the worktree holds the agent's result.
type doneFile struct {
	Exit     int
	Signal   string
	TimedOut bool
	Finished time.Time
	End      executor.End
	Reason   string
}

func donePath(h executor.Handle) string { return h.Worktree + ".done" }

// readDone returns nil when there is no marker.
func readDone(h executor.Handle) (*doneFile, error) {
	b, err := os.ReadFile(donePath(h))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d doneFile
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("corrupt done marker %s: %w", donePath(h), err)
	}
	return &d, nil
}

// writeDone creates the marker once: a second call leaves the first in place.
// It writes a temp file and links it, so a reader never sees a partial file.
func writeDone(h executor.Handle, d doneFile) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	tmp := donePath(h) + ".tmp"
	os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	if err := errors.Join(werr, f.Sync(), f.Close()); err != nil {
		os.Remove(tmp)
		return err
	}
	defer os.Remove(tmp)
	if err := os.Link(tmp, donePath(h)); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return syncDir(filepath.Dir(h.Worktree))
}

func syncDir(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// swap puts the pulled result in place of the worktree: <id> becomes
// <id>.input and <id>.result/work becomes <id>. Every step checks what is
// already done, so a repeat after a crash finishes the job. The original
// worktree's .git file (its link to the clone) goes back into the result,
// whose own .git, if any, is discarded.
func swap(h executor.Handle) error {
	in, res := h.Worktree+".input", h.Worktree+".result"
	if exists(filepath.Join(res, "work")) {
		if !exists(in) {
			if err := os.Rename(h.Worktree, in); err != nil {
				return fmt.Errorf("move worktree aside: %w", err)
			}
		}
		if exists(h.Worktree) {
			return fmt.Errorf("swap: %s exists beside %s", h.Worktree, in)
		}
		if err := os.RemoveAll(filepath.Join(res, "work", ".git")); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(res, "work"), h.Worktree); err != nil {
			return fmt.Errorf("move result into place: %w", err)
		}
	}
	if g := filepath.Join(in, ".git"); exists(g) && exists(h.Worktree) && !exists(filepath.Join(h.Worktree, ".git")) {
		if err := os.Rename(g, filepath.Join(h.Worktree, ".git")); err != nil {
			return fmt.Errorf("restore .git: %w", err)
		}
	}
	return nil
}
