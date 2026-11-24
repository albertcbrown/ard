package tarball

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// Limits caps what Unpack accepts. MaxRatio applies once more than 1 MiB has
// been decompressed.
type Limits struct {
	MaxCompressed int64
	MaxBytes      int64
	MaxFileBytes  int64
	MaxEntries    int
	MaxPathLen    int
	MaxDepth      int
	MaxRatio      int
}

// DefaultLimits returns the limits the executor starts from.
func DefaultLimits() Limits {
	return Limits{
		MaxCompressed: 512 << 20,
		MaxBytes:      2 << 30,
		MaxFileBytes:  100 << 20,
		MaxEntries:    200000,
		MaxPathLen:    1024,
		MaxDepth:      64,
		MaxRatio:      100,
	}
}

// Stats reports what Unpack created.
type Stats struct {
	Entries int
	Bytes   int64
}

const ratioFloor = 1 << 20

// meter counts both streams and records the first limit breach.
type meter struct {
	l          Limits
	compressed int64
	plain      int64
	breach     *Error
}

type countReader struct {
	r          io.Reader
	m          *meter
	decompress bool
}

func (c *countReader) Read(p []byte) (int, error) {
	if c.m.breach != nil {
		return 0, c.m.breach
	}
	n, err := c.r.Read(p)
	m := c.m
	if c.decompress {
		m.plain += int64(n)
		switch {
		case m.l.MaxBytes > 0 && m.plain > m.l.MaxBytes:
			m.breach = newError(ErrLimit, "", "unpacked size over limit")
		case m.l.MaxRatio > 0 && m.plain > ratioFloor && m.plain > int64(m.l.MaxRatio)*m.compressed:
			m.breach = newError(ErrLimit, "", "compression ratio over limit")
		}
	} else {
		m.compressed += int64(n)
		if m.l.MaxCompressed > 0 && m.compressed > m.l.MaxCompressed {
			m.breach = newError(ErrLimit, "", "compressed size over limit")
		}
	}
	if m.breach != nil {
		return 0, m.breach
	}
	return n, err
}

type pendingLink struct{ name, target string }

// Unpack extracts a gzip-compressed tar stream into dest, which must be an
// existing empty directory. All writes go through os.Root. On any error it
// returns at once and leaves a partial tree: the caller must remove dest.
func Unpack(r io.Reader, dest string, l Limits) (Stats, error) {
	var st Stats
	ents, err := os.ReadDir(dest)
	if err != nil {
		return st, err
	}
	if len(ents) > 0 {
		return st, newError(ErrUnsafe, "", "destination not empty")
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return st, err
	}
	defer root.Close()

	m := &meter{l: l}
	gz, err := gzip.NewReader(&countReader{r: r, m: m})
	if err != nil {
		return st, wrap(m, err)
	}
	defer gz.Close()
	tr := tar.NewReader(&countReader{r: gz, m: m, decompress: true})

	seen := map[string]bool{}
	folded := map[string]bool{}
	prefixes := map[string]bool{} // proper ancestor paths of every entry so far
	links := map[string]bool{}
	var pending []pendingLink

	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return st, wrap(m, err)
		}
		if l.MaxEntries > 0 && st.Entries >= l.MaxEntries {
			return st, newError(ErrLimit, "", "too many entries")
		}
		name, err := cleanName(h.Name, l)
		if err != nil {
			return st, err
		}
		if seen[name] || folded[strings.ToLower(name)] {
			return st, newError(ErrUnsafe, name, "duplicate entry")
		}
		parts := strings.Split(name, "/")
		for i := 1; i < len(parts); i++ {
			if links[strings.Join(parts[:i], "/")] {
				return st, newError(ErrUnsafe, name, "path below symlink")
			}
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return st, wrap(m, err)
			}
			if err := root.Chmod(name, 0o755); err != nil {
				return st, err
			}
		case tar.TypeReg:
			if prefixes[name] {
				return st, newError(ErrUnsafe, name, "file replaces directory")
			}
			if l.MaxFileBytes > 0 && h.Size > l.MaxFileBytes {
				return st, newError(ErrLimit, name, "file over limit")
			}
			if err := writeFile(root, tr, name, h, l, m); err != nil {
				return st, err
			}
			st.Bytes += h.Size
		case tar.TypeSymlink:
			if prefixes[name] {
				return st, newError(ErrUnsafe, name, "symlink replaces directory")
			}
			if strings.ContainsRune(h.Linkname, 0) {
				return st, newError(ErrUnsafe, name, "NUL in link target")
			}
			links[name] = true
			pending = append(pending, pendingLink{name, h.Linkname})
		default:
			return st, newError(ErrUnsafe, name, "unsupported entry type")
		}
		seen[name] = true
		folded[strings.ToLower(name)] = true
		for i := 1; i < len(parts); i++ {
			prefixes[strings.Join(parts[:i], "/")] = true
		}
		st.Entries++
	}
	for _, p := range pending {
		if err := root.Symlink(p.target, p.name); err != nil {
			return st, err
		}
	}
	return st, nil
}

func wrap(m *meter, err error) error {
	if m.breach != nil {
		return m.breach
	}
	return fmt.Errorf("tarball: read archive: %w", err)
}

func cleanName(raw string, l Limits) (string, error) {
	name := strings.TrimSuffix(raw, "/")
	if strings.ContainsRune(name, 0) {
		return "", newError(ErrUnsafe, strings.ReplaceAll(name, "\x00", "?"), "NUL in name")
	}
	if l.MaxPathLen > 0 && len(name) > l.MaxPathLen {
		return "", newError(ErrLimit, name, "path too long")
	}
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return "", newError(ErrUnsafe, name, "absolute or empty path")
	}
	parts := strings.Split(name, "/")
	if l.MaxDepth > 0 && len(parts) > l.MaxDepth {
		return "", newError(ErrLimit, name, "path too deep")
	}
	for _, p := range parts {
		switch {
		case p == "" || p == "." || p == "..":
			return "", newError(ErrUnsafe, name, "invalid path segment")
		case strings.EqualFold(p, ".git"):
			return "", newError(ErrUnsafe, name, ".git entry")
		}
	}
	if path.Clean(name) != name {
		return "", newError(ErrUnsafe, name, "path not clean")
	}
	return name, nil
}

func writeFile(root *os.Root, tr io.Reader, name string, h *tar.Header, l Limits, m *meter) error {
	if dir := path.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	mode := os.FileMode(0o644)
	if h.Mode&0o111 != 0 {
		mode = 0o755
	}
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	limit := h.Size
	if l.MaxFileBytes > 0 {
		limit = l.MaxFileBytes + 1
	}
	n, err := io.CopyN(f, tr, limit)
	if err != nil && err != io.EOF {
		f.Close()
		return wrap(m, err)
	}
	if l.MaxFileBytes > 0 && n > l.MaxFileBytes {
		f.Close()
		return newError(ErrLimit, name, "file over limit")
	}
	if err := f.Close(); err != nil {
		return err
	}
	return root.Chmod(name, mode)
}
