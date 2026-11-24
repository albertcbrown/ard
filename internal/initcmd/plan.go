package initcmd

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"slices"
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
	actOverwrite action = "overwrite"
)

// ContextPaths are the optional repo context docs. ard init proposes each
// only when it is missing on main: it never overwrites one or reports a
// conflict, even with --force.
func ContextPaths() []string {
	return []string{".ard/PRODUCT.md", ".ard/VISION.md", ".ard/TECH_STACK.md"}
}

// op is one starter file and what proposing it does.
type op struct {
	File committer.InitFile
	Act  action
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

// plan compares files with existing (main's config tree) and assigns each a
// create, unchanged or overwrite action. A context doc (ContextPaths) that
// exists on main in any form is unchanged, whatever force is. A file that differs without force is
// a conflict; the error names every such path. A different exec bit counts as
// a difference.
func plan(existing fs.FS, files []committer.InitFile, force bool) ([]op, error) {
	ops := make([]op, 0, len(files))
	var conflicts []string
	for _, f := range files {
		o := op{File: f, Act: actCreate}
		fi, err := fs.Stat(existing, f.Path)
		switch {
		case err == nil && slices.Contains(ContextPaths(), f.Path):
			// create-if-absent: present in any form is left alone
			o.Act = actUnchanged
		case err != nil:
			// absent on main
		case fi.IsDir():
			return nil, fmt.Errorf("%s is a directory on main", f.Path)
		default:
			have, err := fs.ReadFile(existing, f.Path)
			if err != nil {
				return nil, fmt.Errorf("read %s from main: %w", f.Path, err)
			}
			haveExec := fi.Mode()&0o111 != 0
			if bytes.Equal(have, f.Data) && haveExec == f.Exec {
				o.Act = actUnchanged
			} else {
				o.Act = actOverwrite
				if !force {
					conflicts = append(conflicts, f.Path)
				}
			}
		}
		ops = append(ops, o)
	}
	if len(conflicts) > 0 {
		return nil, fmt.Errorf("main already has different contents at %s; rerun with --force to propose overwriting them",
			strings.Join(conflicts, ", "))
	}
	return ops, nil
}

// changed returns the files the PR adds or overwrites.
func changed(ops []op) []committer.InitFile {
	var out []committer.InitFile
	for _, o := range ops {
		if o.Act != actUnchanged {
			out = append(out, o.File)
		}
	}
	return out
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
