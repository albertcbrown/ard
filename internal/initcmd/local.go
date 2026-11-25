package initcmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing/fstest"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/setupcmd"
)

// permFunc runs the permission check. A non-nil error means the check could
// not run at all (no repo or credentials).
type permFunc func(ctx context.Context, root string) (setupcmd.Row, error)

// runLocal writes the starter tree into the working tree under root. It
// refuses while .ard/ has uncommitted changes, never deletes a file and
// never writes outside root/.ard.
func runLocal(ctx context.Context, e *cli.Env, o cli.InitOpts, perm permFunc) error {
	root, err := config.RepoRoot(e.ConfigPath)
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	if root, err = filepath.Abs(root); err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return fmt.Errorf("ard init: %s is not the root of a git repository (run it there, or pass --config <repo>/.ard/config.yaml)", root)
	}
	if fi, err := os.Lstat(filepath.Join(root, ".ard")); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("ard init: .ard is a symlink; refusing to write through it")
	}
	dirty, err := gitx.New("").DirtyPaths(ctx, root, ".ard/")
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	if len(dirty) > 0 {
		return fmt.Errorf("ard init: .ard/ has uncommitted changes, commit or stash them first:\n  %s", strings.Join(dirty, "\n  "))
	}

	files, err := starter()
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	existing, err := readLocal(root)
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	var lock Lock
	if data, err := fs.ReadFile(existing, LockPath); err == nil {
		var warns []string
		lock, warns = ParseLock(data)
		for _, w := range warns {
			fmt.Fprintln(e.Stderr, "warning: "+w)
		}
	}
	pl, err := plan(existing, files, lock)
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	toWrite := changed(pl.Ops)
	if len(toWrite) > 0 {
		data, err := withLock(lock, pl.Ops, o.Version).Marshal()
		if err != nil {
			return fmt.Errorf("ard init: %w", err)
		}
		toWrite = append(toWrite, committer.InitFile{Path: LockPath, Data: data})
	}
	merged, err := overlay(existing, toWrite)
	if err != nil {
		return fmt.Errorf("ard init: %w", err)
	}
	if _, err := config.LoadFS(merged, root); err != nil {
		return fmt.Errorf("ard init: the resulting configuration is invalid, nothing was written:\n%w", err)
	}

	printOps(e, pl.Ops)
	printLeftAlone(e, pl)
	if !e.DryRun {
		for _, f := range toWrite {
			if err := writeLocal(root, f); err != nil {
				return fmt.Errorf("ard init: write %s: %w", f.Path, err)
			}
		}
	}

	if perm != nil {
		row, err := perm(ctx, root)
		switch {
		case err != nil:
			fmt.Fprintf(e.Stdout, "warning: permissions not checked: %v\n", err)
		case row.Status != setupcmd.StatusOK:
			fmt.Fprintf(e.Stdout, "warning: permissions: %s\n", row.Detail)
		default:
			fmt.Fprintf(e.Stdout, "permissions: %s\n", row.Detail)
		}
	}

	switch {
	case e.DryRun:
		fmt.Fprintln(e.Stdout, "dry-run: nothing was written")
	case len(toWrite) == 0:
		fmt.Fprintln(e.Stdout, "nothing to change")
		fmt.Fprintln(e.Stdout, "next: run `ard setup`")
	default:
		fmt.Fprintf(e.Stdout, "wrote %d files under .ard/\n", len(toWrite))
		fmt.Fprintln(e.Stdout, "next: commit .ard/, merge it to main, then run `ard setup`")
	}
	e.Log.InfoContext(ctx, "init.done", "mode", "local", "files", len(toWrite), "dry_run", e.DryRun)
	return nil
}

// readLocal copies the regular files under root/.ard into memory.
func readLocal(root string) (fs.FS, error) {
	m := fstest.MapFS{}
	base := filepath.Join(root, ".ard")
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == base {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		m[filepath.ToSlash(rel)] = &fstest.MapFile{Data: data, Mode: info.Mode()}
		return nil
	})
	return m, err
}

// writeLocal writes f atomically under root. The path must be a clean path
// under .ard/ and no component may be a symlink.
func writeLocal(root string, f committer.InitFile) error {
	if err := committer.CheckInitPath(f.Path); err != nil || path.Clean(f.Path) != f.Path || !strings.HasPrefix(f.Path, ".ard/") {
		return fmt.Errorf("path %q is outside .ard/", f.Path)
	}
	dir := root
	parts := strings.Split(f.Path, "/")
	for _, c := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, c)
		fi, err := os.Lstat(dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Mkdir(dir, 0o755); err != nil {
				return err
			}
		case err != nil:
			return err
		case fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir():
			return fmt.Errorf("%s is a symlink or not a directory", dir)
		}
	}
	target := filepath.Join(dir, parts[len(parts)-1])
	if fi, err := os.Lstat(target); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", target)
	}
	tmp, err := os.CreateTemp(dir, ".ard-init-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	mode := os.FileMode(0o644)
	if f.Exec {
		mode = 0o755
	}
	if _, err := tmp.Write(f.Data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}
