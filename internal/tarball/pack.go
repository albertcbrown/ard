package tarball

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Root is one tree to pack. Name is the archive's top-level directory (one
// path segment) and Dir the absolute directory on disk. When Files is
// non-empty only those regular files directly under Dir are packed; a listed
// file that does not exist is left out.
type Root struct {
	Name  string
	Dir   string
	Files []string
}

// PackStats reports what Pack wrote. Skipped counts sockets, FIFOs and
// devices that were left out.
type PackStats struct {
	Entries int
	Bytes   int64
	Skipped int
}

func validSegment(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\\x00") && !strings.EqualFold(s, ".git")
}

func hasGit(rel string) bool {
	for _, p := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.EqualFold(p, ".git") {
			return true
		}
	}
	return false
}

func tarMode(m fs.FileMode) int64 {
	if m.IsDir() || m&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// Pack writes the roots as a gzip-compressed tar stream. Symlinks are stored
// as links and never followed, entries with a .git path component are left
// out, and special files are skipped and counted. The output is deterministic.
func Pack(w io.Writer, roots []Root) (PackStats, error) {
	var st PackStats
	for _, r := range roots {
		if !validSegment(r.Name) {
			return st, fmt.Errorf("tarball: invalid root name %q", r.Name)
		}
		for _, f := range r.Files {
			if !validSegment(f) {
				return st, fmt.Errorf("tarball: invalid file name %q", f)
			}
		}
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, r := range roots {
		if err := packRoot(tw, r, &st); err != nil {
			return st, err
		}
	}
	if err := tw.Close(); err != nil {
		return st, err
	}
	if err := gz.Close(); err != nil {
		return st, err
	}
	return st, nil
}

func packRoot(tw *tar.Writer, r Root, st *PackStats) error {
	info, err := os.Lstat(r.Dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("tarball: root %q: not a directory", r.Name)
	}
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: r.Name + "/", Mode: 0o755, Format: tar.FormatPAX}); err != nil {
		return err
	}
	st.Entries++
	if len(r.Files) > 0 {
		for _, f := range r.Files {
			fi, err := os.Lstat(filepath.Join(r.Dir, f))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if err := packEntry(tw, filepath.Join(r.Dir, f), r.Name+"/"+f, fi, st); err != nil {
				return err
			}
		}
		return nil
	}
	return filepath.WalkDir(r.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(r.Dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if hasGit(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		return packEntry(tw, p, r.Name+"/"+filepath.ToSlash(rel), fi, st)
	})
}

func packEntry(tw *tar.Writer, path, name string, fi fs.FileInfo, st *PackStats) error {
	h := &tar.Header{Name: name, Mode: tarMode(fi.Mode()), ModTime: time.Time{}, Format: tar.FormatPAX}
	switch {
	case fi.IsDir():
		h.Typeflag = tar.TypeDir
		h.Name += "/"
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		h.Typeflag = tar.TypeSymlink
		h.Linkname = target
		h.Mode = 0o777
	case fi.Mode().IsRegular():
		return packFile(tw, path, h, fi, st)
	default:
		st.Skipped++
		return nil
	}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	st.Entries++
	return nil
}

func packFile(tw *tar.Writer, path string, h *tar.Header, fi fs.FileInfo, st *PackStats) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if cur, err := f.Stat(); err != nil || !cur.Mode().IsRegular() {
		return fmt.Errorf("tarball: %s changed while packing", h.Name)
	}
	h.Typeflag = tar.TypeReg
	h.Size = fi.Size()
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	n, err := io.Copy(tw, io.LimitReader(f, h.Size))
	if err != nil {
		return err
	}
	if n != h.Size {
		return fmt.Errorf("tarball: %s changed size while packing", h.Name)
	}
	var extra [1]byte
	if m, _ := f.Read(extra[:]); m > 0 {
		return fmt.Errorf("tarball: %s changed size while packing", h.Name)
	}
	st.Entries++
	st.Bytes += n
	return nil
}
