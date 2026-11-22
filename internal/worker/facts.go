package worker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/redact"
)

const binarySniff = 8 << 10

// facts gathers what Check needs for the step whose worktree is wt and whose
// base commit is base. The worktree belongs to the agent, so nothing in it is
// trusted: the changes come from a gitx.Snapshot (the controller's own
// repository, a private index, no worktree config or attributes), and every
// file is read through os.Root, so a symlink is never followed. The bytes read
// here are the bytes the code lane commits: it reads the same paths from the
// same disk, and Check passes it only the files it saw. A scanner error marks
// the file ScanFailed (fail closed).
//
// The pattern scan keeps only findings on lines this step added (a new file
// is all added), so a worker that edits a file which already holds a
// credential-shaped fixture is not held for lines it did not write.
//
// prLines counts the changed lines of the whole PR: the diff against the
// merge base of mainSHA and base, with the loop's and close-out's paths left
// out.
func (l *Loop) facts(ctx context.Context, issue int, wt, base, mainSHA string) (changes []Change, prLines int, err error) {
	g := l.d.Git
	snap, err := g.Snapshot(ctx, l.d.RepoDir, wt, base)
	if err != nil {
		return nil, 0, fmt.Errorf("snapshot: %w", err)
	}
	defer snap.Close()
	chs, err := snap.Changed(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("list changes: %w", err)
	}
	added, err := snap.AddedLines(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("list added lines: %w", err)
	}
	prBase := base
	if mainSHA != "" && mainSHA != base {
		if mb, err := g.MergeBase(ctx, l.d.RepoDir, mainSHA, base); err == nil && mb != "" {
			prBase = mb
		}
	}
	prStat, err := snap.Numstat(ctx, prBase)
	if err != nil {
		return nil, 0, fmt.Errorf("numstat of the PR: %w", err)
	}
	stat := map[string]gitx.NumStat{}
	for _, ns := range prStat {
		stat[ns.Path] = ns
		if !excluded(issue, ns.Path) && !strings.HasPrefix(ns.Path, ".ard/") {
			prLines += ns.Added + ns.Deleted
		}
	}
	root, err := os.OpenRoot(wt)
	if err != nil {
		return nil, 0, fmt.Errorf("open worktree: %w", err)
	}
	defer root.Close()
	secrets := l.d.Secrets()
	for _, ch := range chs {
		if excluded(issue, ch.Path) {
			continue
		}
		c := Change{Path: ch.Path, Deleted: ch.Deleted}
		if !ch.Deleted {
			l.fileFacts(ctx, root, &c, secrets, added[ch.Path])
			switch ch.Mode {
			case "160000":
				c.Kind = NestedRepo
			case "120000":
				c.Kind = Symlink
			}
			if ns, ok := stat[ch.Path]; ok && ns.Binary {
				c.Binary = true
			}
		}
		changes = append(changes, c)
	}
	return changes, prLines, nil
}

// fileFacts fills the on-disk facts of one changed file. Only the added line ranges are pattern-scanned.
func (l *Loop) fileFacts(ctx context.Context, root *os.Root, c *Change, secrets *redact.Secrets, added []gitx.LineRange) {
	fi, err := root.Lstat(c.Path)
	switch {
	case err != nil:
		c.Kind = Other
		return
	case fi.Mode()&fs.ModeSymlink != 0:
		c.Kind = Symlink
		return
	case !fi.Mode().IsRegular():
		c.Kind = Other
		return
	}
	c.Kind, c.Size = Regular, fi.Size()
	if fi.Mode()&0o111 != 0 {
		c.Kind = Exec
	}
	if c.Size > MaxFileSize {
		return
	}
	f, err := root.Open(c.Path)
	if err != nil {
		c.ScanFailed = true
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil || len(data) > MaxFileSize {
		c.ScanFailed = true
		return
	}
	if bytes.IndexByte(data[:min(len(data), binarySniff)], 0) >= 0 {
		c.Binary = true
		return
	}
	c.Known = secrets.Names(data)
	found, err := l.d.Scanner.Scan(ctx, bytes.NewReader(data))
	if err != nil {
		c.ScanFailed = true
	}
	for _, f := range found {
		if inRanges(f.Line, added) {
			c.Patterns = append(c.Patterns, f)
		}
	}
}

func inRanges(line int, rs []gitx.LineRange) bool {
	for _, r := range rs {
		if line >= r.From && line <= r.To {
			return true
		}
	}
	return false
}
