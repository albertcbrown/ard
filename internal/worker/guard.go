package worker

import (
	"fmt"
	"strings"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/redact"
)

// Fixed limits of one job's diff. The line thresholds are per worker role
// (Lines).
const (
	MaxFiles    = 256     // changed files in one step
	MaxFileSize = 1 << 20 // bytes in one file
	maxShown    = 3       // reject entries named in a hold reason
	maxPathRune = 80
)

// FileKind is what a changed path is on disk.
type FileKind int

// File kinds.
const (
	Regular FileKind = iota
	Exec
	Symlink
	NestedRepo
	Other
)

// Change is one changed path with the facts the guard needs. facts.go builds
// it; Check is pure.
type Change struct {
	Path    string
	Deleted bool
	Kind    FileKind
	Size    int64
	Binary  bool
	// Known names the known credential values found in the whole new content.
	Known []string
	// Patterns are the pattern-scanner findings on lines this job added.
	Patterns []redact.Finding
	// ScanFailed means a scanner could not run on the file (fail closed).
	ScanFailed bool
	// Copy is a controller-private copy of exactly the bytes that were scanned.
	// The code lane commits it, not the agent's file, which a surviving
	// process could change after the scan. "" for a deletion or a file that
	// was not scanned.
	Copy string
}

// Reject is one reason a change cannot be committed. It never holds file
// content.
type Reject struct {
	Path string
	Line int // > 0 for a pattern finding
	Why  string
}

func (r Reject) String() string {
	p := clipRunes(r.Path, maxPathRune)
	switch {
	case p == "":
		return r.Why
	case r.Line > 0:
		return fmt.Sprintf("%s:%d: %s", p, r.Line, r.Why)
	}
	return p + ": " + r.Why
}

// Lines are a worker role's thresholds on the whole PR's changed lines
// (diff_warn_lines and diff_max_lines). Above Warn the PR proceeds with a
// note; above Max it is held for a human.
type Lines struct {
	Warn int
	Max  int
}

// Verdict is the guard's decision on one step's changes.
type Verdict struct {
	Commit  []Change // changes the code lane may commit
	Rejects []Reject
	Lines   int    // changed lines in the whole PR
	NoCode  bool   // nothing to commit
	Hold    string // non-empty: a human must look; nothing is pushed
	// Large is a fixed note, set when the PR is over the warn threshold but
	// not held: "Large change: N changed lines (warn at W)". It never quotes
	// content.
	Large string
}

// excluded reports paths the loop and close-out own: the loop's inputs and
// .ard/<issue#>/. They are never code, so they are neither committed nor
// rejected here.
func excluded(issue int, p string) bool {
	return strings.HasPrefix(p, ".ard-input/") || strings.HasPrefix(p, fmt.Sprintf(".ard/%d/", issue))
}

// Check decides one step. changes is every changed path of the step
// (including the excluded ones, which Check skips), prLines the changed lines
// of the whole PR, branchExists whether the branch is already on the remote
// and lim the role's line thresholds.
// Hold priority: rejects, then the line limit, then no code on a new branch.
func Check(issue int, changes []Change, prLines int, branchExists bool, lim Lines) Verdict {
	v := Verdict{Lines: prLines}
	n := 0
	for _, c := range changes {
		if excluded(issue, c.Path) {
			continue
		}
		n++
		if rs := checkOne(issue, c); len(rs) > 0 {
			v.Rejects = append(v.Rejects, rs...)
			continue
		}
		v.Commit = append(v.Commit, c)
	}
	if n > MaxFiles {
		v.Rejects = append(v.Rejects, Reject{Why: fmt.Sprintf("%d changed files, limit %d", n, MaxFiles)})
	}
	v.NoCode = len(v.Commit) == 0
	switch {
	case len(v.Rejects) > 0:
		v.Hold = holdReason(v.Rejects)
	case prLines > lim.Max:
		v.Hold = fmt.Sprintf("diff guard: %d changed lines, limit %d", prLines, lim.Max)
	case v.NoCode && !branchExists:
		v.Hold = "diff guard: no code changes"
	}
	if v.Hold == "" && prLines > lim.Warn {
		v.Large = fmt.Sprintf("Large change: %d changed lines (warn at %d)", prLines, lim.Warn)
	}
	return v
}

func checkOne(issue int, c Change) []Reject {
	if err := committer.CheckCodePath(issue, c.Path); err != nil {
		return []Reject{{Path: c.Path, Why: err.Error()}}
	}
	if c.Deleted {
		return nil
	}
	switch c.Kind {
	case Symlink:
		return []Reject{{Path: c.Path, Why: "symlink"}}
	case NestedRepo:
		return []Reject{{Path: c.Path, Why: "nested repository"}}
	case Other:
		return []Reject{{Path: c.Path, Why: "not a regular file"}}
	}
	var out []Reject
	if c.Size > MaxFileSize {
		out = append(out, Reject{Path: c.Path, Why: "larger than 1 MiB"})
	}
	if c.Binary {
		out = append(out, Reject{Path: c.Path, Why: "binary file"})
	}
	if c.ScanFailed {
		out = append(out, Reject{Path: c.Path, Why: "secret scan could not run"})
	}
	for _, name := range c.Known {
		out = append(out, Reject{Path: c.Path, Why: "contains the value of " + name})
	}
	for _, f := range c.Patterns {
		out = append(out, Reject{Path: c.Path, Line: f.Line, Why: "secret scan rule " + f.Rule})
	}
	return out
}

// holdReason formats the first rejects as one line that never quotes content.
func holdReason(rs []Reject) string {
	var parts []string
	for i, r := range rs {
		if i == maxShown {
			break
		}
		parts = append(parts, r.String())
	}
	s := "diff guard: " + strings.Join(parts, "; ")
	if len(rs) > maxShown {
		s += fmt.Sprintf("; and %d more", len(rs)-maxShown)
	}
	return s
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
