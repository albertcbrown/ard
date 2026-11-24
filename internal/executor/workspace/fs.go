package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
)

// ChmodReadOnly removes write permission recursively (host mode ReadOnly).
func ChmodReadOnly(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink != 0 {
			return err
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		return os.Chmod(p, st.Mode().Perm()&^0o222)
	})
}

// ChmodWritable restores owner write permission so the tree can be removed.
// Directories are made writable and searchable before they are read, so a
// 0000 or 0555 directory (the Go module cache) is handled. Symlinks are
// never followed or chmodded.
func ChmodWritable(root string) {
	st, err := os.Lstat(root)
	if err != nil || st.Mode()&fs.ModeSymlink != 0 {
		return
	}
	if !st.IsDir() {
		if st.Mode().IsRegular() {
			os.Chmod(root, st.Mode().Perm()|0o200)
		}
		return
	}
	os.Chmod(root, st.Mode().Perm()|0o700)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		ChmodWritable(filepath.Join(root, e.Name()))
	}
}

// RemoveAllWritable removes path even when it holds read-only trees.
func RemoveAllWritable(path string) error {
	ChmodWritable(path)
	return os.RemoveAll(path)
}
