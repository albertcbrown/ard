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
// base commit is base. Every file is read through os.Root, so a symlink is
// never followed. A scanner error marks the file ScanFailed (fail closed).
//
// The pattern scan keeps only findings on lines this step added: the added
// ranges of a tracked file, every line of an untracked one. A worker that
// edits a file which already holds a credential-shaped fixture is therefore
// not held for lines it did not write.
//
// prLines counts the changed lines of the whole PR: the diff against the
// merge base of mainSHA and base, plus every line of an untracked file, with
// the loop's and close-out's paths left out.
func (l *Loop) facts(ctx context.Context, issue int, wt, base, mainSHA string) (changes []Change, prLines int, err error) {
	g := l.d.Git
	chs, err := g.ChangedSince(ctx, wt, base)
	if err != nil {
		return nil, 0, fmt.Errorf("list changes: %w", err)
	}
	added, err := g.AddedLines(ctx, wt, base)
	if err != nil {
		return nil, 0, fmt.Errorf("list added lines: %w", err)
	}
	tracked, err := g.DiffNumstat(ctx, wt, base)
	if err != nil {
		return nil, 0, fmt.Errorf("numstat: %w", err)
	}
	isTracked := map[string]gitx.NumStat{}
	for _, ns := range tracked {
		isTracked[ns.Path] = ns
	}
	prBase := base
	if mainSHA != "" && mainSHA != base {
		if mb, err := g.MergeBase(ctx, wt, mainSHA, base); err == nil && mb != "" {
			prBase = mb
		}
	}
	var prStat []gitx.NumStat
	if prBase == base {
		prStat = tracked
	} else if prStat, err = g.DiffNumstat(ctx, wt, prBase); err != nil {
		return nil, 0, fmt.Errorf("numstat of the PR: %w", err)
	}
	for _, ns := range prStat {
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
		c := Change{Path: strings.TrimSuffix(ch.Path, "/"), Deleted: ch.Deleted}
		switch {
		case ch.Deleted:
		case strings.HasSuffix(ch.Path, "/"): // git lists a nested repository as "dir/"
			c.Kind = NestedRepo
		default:
			ns, tr := isTracked[ch.Path]
			lines := l.fileFacts(ctx, root, &c, secrets, tr, added[ch.Path])
			if tr && ns.Binary {
				c.Binary = true
			}
			if !tr && !c.Binary {
				prLines += lines
			}
		}
		changes = append(changes, c)
	}
	return changes, prLines, nil
}

// fileFacts fills the on-disk facts of one changed file and returns its line
// count. tracked says the file is in the base commit, so only the added line
// ranges are pattern-scanned.
func (l *Loop) fileFacts(ctx context.Context, root *os.Root, c *Change, secrets *redact.Secrets, tracked bool, added []gitx.LineRange) int {
	fi, err := root.Lstat(c.Path)
	switch {
	case err != nil:
		c.Kind = Other
		return 0
	case fi.Mode()&fs.ModeSymlink != 0:
		c.Kind = Symlink
		return 0
	case !fi.Mode().IsRegular():
		c.Kind = Other
		return 0
	}
	c.Kind, c.Size = Regular, fi.Size()
	if fi.Mode()&0o111 != 0 {
		c.Kind = Exec
	}
	if c.Size > MaxFileSize {
		return 0
	}
	f, err := root.Open(c.Path)
	if err != nil {
		c.ScanFailed = true
		return 0
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil || len(data) > MaxFileSize {
		c.ScanFailed = true
		return 0
	}
	if bytes.IndexByte(data[:min(len(data), binarySniff)], 0) >= 0 {
		c.Binary = true
		return 0
	}
	c.Known = secrets.Names(data)
	found, err := l.d.Scanner.Scan(ctx, bytes.NewReader(data))
	if err != nil {
		c.ScanFailed = true
	}
	for _, f := range found {
		if !tracked || inRanges(f.Line, added) {
			c.Patterns = append(c.Patterns, f)
		}
	}
	return countLines(data)
}

func inRanges(line int, rs []gitx.LineRange) bool {
	for _, r := range rs {
		if line >= r.From && line <= r.To {
			return true
		}
	}
	return false
}

func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := bytes.Count(b, []byte{'\n'})
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}
