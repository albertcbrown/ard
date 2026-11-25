package initcmd

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
	"testing/fstest"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/roles"
)

// action says what the PR does to one path on main.
type action string

const (
	actCreate    action = "add"
	actUnchanged action = "unchanged"
	actUpdate    action = "update"
	actEdited    action = "edited" // differs from the starter and from the lock: left alone
)

// ContextPaths are the optional repo context docs. ard init proposes each
// only when it is missing on main: it never updates one, whatever the lock says.
func ContextPaths() []string {
	return []string{".ard/PRODUCT.md", ".ard/VISION.md", ".ard/TECH_STACK.md"}
}

// op is one starter file and what proposing it does.
type op struct {
	File committer.InitFile
	Act  action
	Diff string // actEdited: main against the starter
}

// starter lists the embedded tree as files under .ard/, in sorted path
// order. Files named in roles.ExecutableNames are executable.
func starter() ([]committer.InitFile, error) {
	var files []committer.InitFile
	err := fs.WalkDir(roles.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := roles.FS.ReadFile(p)
		if err != nil {
			return err
		}
		files = append(files, committer.InitFile{
			Path: ".ard/" + p,
			Data: data,
			Exec: slices.Contains(roles.ExecutableNames, path.Base(p)),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read starter files: %w", err)
	}
	// WalkDir sorts by entry name, so "trufflehog/" would precede "trufflehog-history/".
	slices.SortFunc(files, func(a, b committer.InitFile) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// Result is the plan for one run.
type Result struct {
	Ops []op
	// Dropped lists files the lock records that the starter no longer ships.
	// They are reported and never deleted.
	Dropped []string
}

// plan compares files with existing (main's config tree) and the lock. Per
// file: a context doc is created only when absent; an absent file is created;
// one identical to the starter (content and exec bit) is unchanged; one whose
// hash equals its lock entry is updated; any other is edited and never
// touched, with a diff of main against the starter. No lock or no entry means
// edited.
func plan(existing fs.FS, files []committer.InitFile, lock Lock) (Result, error) {
	res := Result{Ops: make([]op, 0, len(files))}
	inStarter := map[string]bool{}
	for _, f := range files {
		inStarter[f.Path] = true
		o := op{File: f, Act: actCreate}
		fi, err := fs.Stat(existing, f.Path)
		switch {
		case err == nil && slices.Contains(ContextPaths(), f.Path):
			// create-if-absent: present in any form is left alone
			o.Act = actUnchanged
		case err != nil:
			// absent on main
		case fi.IsDir():
			return Result{}, fmt.Errorf("%s is a directory on main", f.Path)
		default:
			have, err := fs.ReadFile(existing, f.Path)
			if err != nil {
				return Result{}, fmt.Errorf("read %s from main: %w", f.Path, err)
			}
			haveExec := fi.Mode()&0o111 != 0
			switch want, ok := lock.Files[f.Path]; {
			case bytes.Equal(have, f.Data) && haveExec == f.Exec:
				o.Act = actUnchanged
			case ok && want == hashOf(have):
				o.Act = actUpdate
			default:
				o.Act = actEdited
				o.Diff = unifiedDiff(f.Path, string(have), string(f.Data))
			}
		}
		res.Ops = append(res.Ops, o)
	}
	for p := range lock.Files {
		if !inStarter[p] && !slices.Contains(ContextPaths(), p) {
			res.Dropped = append(res.Dropped, p)
		}
	}
	sort.Strings(res.Dropped)
	return res, nil
}

// changed returns the files the PR adds or updates.
func changed(ops []op) []committer.InitFile {
	var out []committer.InitFile
	for _, o := range ops {
		if o.Act == actCreate || o.Act == actUpdate {
			out = append(out, o.File)
		}
	}
	return out
}

// unifiedDiff is a plain line diff (LCS) of have against want, with the whole
// file as one hunk. It is for the operator to read, not to apply.
func unifiedDiff(name, have, want string) string {
	a, b := splitLines(have), splitLines(want)
	n, m := len(a), len(b)
	if n*m > 4_000_000 {
		return fmt.Sprintf("--- %s (main)\n+++ %s (starter)\n(too large to diff)\n", name, name)
	}
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s (main)\n+++ %s (starter)\n", name, name)
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			sb.WriteString(" " + a[i] + "\n")
			i++
			j++
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			sb.WriteString("+" + b[j] + "\n")
			j++
		default:
			sb.WriteString("-" + a[i] + "\n")
			i++
		}
	}
	return sb.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// overlay copies existing (files and directories) into a MapFS and lays files
// on top, so config validation sees exactly the tree the PR would produce.
// Directories are copied as well as files: an empty role directory on main
// must still fail validation.
func overlay(existing fs.FS, files []committer.InitFile) (fstest.MapFS, error) {
	m := fstest.MapFS{}
	err := fs.WalkDir(existing, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == "." {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			m[p] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
			return nil
		}
		data, err := fs.ReadFile(existing, p)
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o644)
		if fi.Mode()&0o111 != 0 {
			mode = 0o755
		}
		m[p] = &fstest.MapFile{Data: data, Mode: mode}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read main's config tree: %w", err)
	}
	for _, f := range files {
		mode := fs.FileMode(0o644)
		if f.Exec {
			mode = 0o755
		}
		m[f.Path] = &fstest.MapFile{Data: f.Data, Mode: mode}
	}
	return m, nil
}
