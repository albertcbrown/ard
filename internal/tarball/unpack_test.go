package tarball

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type ent struct {
	name, link string
	typ        byte
	mode       int64
	body       string
}

func buildTar(t testing.TB, ents []ent) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range ents {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Linkname: e.link, Typeflag: typ, Mode: e.mode}
		if typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func smallLimits() Limits {
	return Limits{MaxCompressed: 1 << 20, MaxBytes: 1 << 20, MaxFileBytes: 1 << 10, MaxEntries: 8, MaxPathLen: 64, MaxDepth: 4, MaxRatio: 100}
}

// setup returns a parent dir holding dest and a sentinel sibling dir.
func setup(t testing.TB) (parent, dest, sentinel string) {
	parent = t.TempDir()
	dest = filepath.Join(parent, "dest")
	sentinel = filepath.Join(parent, "sentinel")
	for _, d := range []string{dest, sentinel} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func checkContained(t testing.TB, parent string) {
	t.Helper()
	top, _ := os.ReadDir(parent)
	if len(top) != 2 {
		t.Fatalf("parent has %d entries, want dest and sentinel only", len(top))
	}
	if s, _ := os.ReadDir(filepath.Join(parent, "sentinel")); len(s) != 0 {
		t.Fatal("sentinel dir was written to")
	}
}

type zeroReader struct{ n int64 }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > z.n {
		p = p[:z.n]
	}
	clear(p)
	z.n -= int64(len(p))
	return len(p), nil
}

func bombArchive(size int64) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		gz := gzip.NewWriter(pw)
		tw := tar.NewWriter(gz)
		err := tw.WriteHeader(&tar.Header{Name: "big", Typeflag: tar.TypeReg, Mode: 0o644, Size: size})
		if err == nil {
			_, err = io.Copy(tw, &zeroReader{n: size})
		}
		if err == nil {
			err = tw.Close()
		}
		if err == nil {
			err = gz.Close()
		}
		pw.CloseWithError(err)
	}()
	return pr
}

func rejectCases() map[string]struct {
	ents []ent
	lim  func(*Limits)
	want error
} {
	type c = struct {
		ents []ent
		lim  func(*Limits)
		want error
	}
	return map[string]c{
		"dotdot":              {ents: []ent{{name: "../x", body: "x"}}, want: ErrUnsafe},
		"absolute":            {ents: []ent{{name: "/abs", body: "x"}}, want: ErrUnsafe},
		"embedded dotdot":     {ents: []ent{{name: "a/../../x", body: "x"}}, want: ErrUnsafe},
		"dot segment":         {ents: []ent{{name: "a/./b", body: "x"}}, want: ErrUnsafe},
		"empty segment":       {ents: []ent{{name: "a//b", body: "x"}}, want: ErrUnsafe},
		"symlink then file":   {ents: []ent{{name: "a", typ: tar.TypeSymlink, link: ".."}, {name: "a/f", body: "x"}}, want: ErrUnsafe},
		"file then symlink":   {ents: []ent{{name: "a/f", body: "x"}, {name: "a", typ: tar.TypeSymlink, link: ".."}}, want: ErrUnsafe},
		"hardlink":            {ents: []ent{{name: "h", typ: tar.TypeLink, link: "/etc/passwd"}}, want: ErrUnsafe},
		"char device":         {ents: []ent{{name: "c", typ: tar.TypeChar}}, want: ErrUnsafe},
		"block device":        {ents: []ent{{name: "b", typ: tar.TypeBlock}}, want: ErrUnsafe},
		"fifo":                {ents: []ent{{name: "p", typ: tar.TypeFifo}}, want: ErrUnsafe},
		"duplicate":           {ents: []ent{{name: "f", body: "1"}, {name: "f", body: "2"}}, want: ErrUnsafe},
		"dir then file":       {ents: []ent{{name: "d/", typ: tar.TypeDir}, {name: "d", body: "x"}}, want: ErrUnsafe},
		"case collision":      {ents: []ent{{name: "README", body: "1"}, {name: "readme", body: "2"}}, want: ErrUnsafe},
		"git config":          {ents: []ent{{name: ".git/config", body: "x"}}, want: ErrUnsafe},
		"nested git":          {ents: []ent{{name: "sub/.git", body: "x"}}, want: ErrUnsafe},
		"upper git":           {ents: []ent{{name: "x/.GIT", body: "x"}}, want: ErrUnsafe},
		"too many entries":    {ents: []ent{{name: "a", body: ""}, {name: "b"}, {name: "c"}}, lim: func(l *Limits) { l.MaxEntries = 2 }, want: ErrLimit},
		"file over limit":     {ents: []ent{{name: "f", body: strings.Repeat("x", 2000)}}, want: ErrLimit},
		"total over limit":    {ents: []ent{{name: "f", body: strings.Repeat("x", 900)}, {name: "g", body: strings.Repeat("y", 900)}}, lim: func(l *Limits) { l.MaxBytes = 2048 }, want: ErrLimit},
		"compressed over":     {ents: []ent{{name: "f", body: "x"}}, lim: func(l *Limits) { l.MaxCompressed = 10 }, want: ErrLimit},
		"path too long":       {ents: []ent{{name: strings.Repeat("a", 100), body: "x"}}, want: ErrLimit},
		"too deep":            {ents: []ent{{name: "a/b/c/d/e/f", body: "x"}}, want: ErrLimit},
		"symlink dir overlap": {ents: []ent{{name: "a/b/c", body: "x"}, {name: "a/b", typ: tar.TypeSymlink, link: "/"}}, want: ErrUnsafe},
	}
}

func TestUnpackRejects(t *testing.T) {
	for name, c := range rejectCases() {
		t.Run(name, func(t *testing.T) {
			parent, dest, _ := setup(t)
			l := smallLimits()
			if c.lim != nil {
				c.lim(&l)
			}
			_, err := Unpack(bytes.NewReader(buildTar(t, c.ents)), dest, l)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			checkContained(t, parent)
		})
	}
}

func TestCleanNameNUL(t *testing.T) {
	// archive/tar cuts ustar names at a NUL, so this guards the PAX path.
	if _, err := cleanName("a\x00b", smallLimits()); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("err = %v, want ErrUnsafe", err)
	}
}

func TestUnpackGzipBomb(t *testing.T) {
	parent, dest, _ := setup(t)
	l := DefaultLimits()
	l.MaxFileBytes = 2 << 30
	l.MaxBytes = 4 << 30
	size := int64(1 << 30)
	if testing.Short() {
		size = 64 << 20
	}
	_, err := Unpack(bombArchive(size), dest, l)
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("err = %v, want ErrLimit", err)
	}
	checkContained(t, parent)
}

func TestUnpackDest(t *testing.T) {
	data := buildTar(t, []ent{{name: "f", body: "x"}})
	_, dest, _ := setup(t)
	os.WriteFile(filepath.Join(dest, "old"), nil, 0o644)
	if _, err := Unpack(bytes.NewReader(data), dest, smallLimits()); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("non-empty dest: %v", err)
	}
	dir := t.TempDir()
	if _, err := Unpack(bytes.NewReader(data), filepath.Join(dir, "missing"), smallLimits()); err == nil {
		t.Fatal("missing dest accepted")
	}
	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o644)
	if _, err := Unpack(bytes.NewReader(data), file, smallLimits()); err == nil {
		t.Fatal("file dest accepted")
	}
}

func TestUnpackBadInput(t *testing.T) {
	good := buildTar(t, []ent{{name: "f", body: strings.Repeat("x", 500)}})
	for name, data := range map[string][]byte{
		"garbage":   []byte("this is not gzip"),
		"empty":     nil,
		"truncated": good[:len(good)/2],
	} {
		t.Run(name, func(t *testing.T) {
			_, dest, _ := setup(t)
			if _, err := Unpack(bytes.NewReader(data), dest, smallLimits()); err == nil {
				t.Fatal("no error")
			}
		})
	}
}

func TestUnpackModes(t *testing.T) {
	_, dest, _ := setup(t)
	data := buildTar(t, []ent{
		{name: "suid", mode: 0o4755, body: "x"},
		{name: "private", mode: 0o600, body: "x"},
		{name: "d/", typ: tar.TypeDir, mode: 0o777},
	})
	st, err := Unpack(bytes.NewReader(data), dest, smallLimits())
	if err != nil {
		t.Fatal(err)
	}
	if st.Entries != 3 || st.Bytes != 2 {
		t.Fatalf("stats = %+v", st)
	}
	for name, want := range map[string]fs.FileMode{"suid": 0o755, "private": 0o644, "d": fs.ModeDir | 0o755} {
		fi, err := os.Lstat(filepath.Join(dest, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode() != want {
			t.Errorf("%s mode = %v, want %v", name, fi.Mode(), want)
		}
	}
}

func FuzzUnpack(f *testing.F) {
	for _, c := range rejectCases() {
		f.Add(buildTar(f, c.ents))
	}
	f.Add(buildTar(f, []ent{{name: "ok/f", mode: 0o644, body: "hi"}, {name: "l", typ: tar.TypeSymlink, link: "ok/f"}}))
	f.Fuzz(func(t *testing.T, data []byte) {
		parent, dest, _ := setup(t)
		_, _ = Unpack(bytes.NewReader(data), dest, smallLimits())
		// Also exercise the tar layer when the fuzzer's bytes are not gzip.
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		gz.Write(data)
		gz.Close()
		_, dest2, _ := setup(t)
		_, _ = Unpack(&buf, dest2, smallLimits())
		_ = dest2
		for _, p := range []string{parent} {
			top, _ := os.ReadDir(p)
			if len(top) != 2 {
				t.Fatalf("entries outside dest in %s", p)
			}
			if s, _ := os.ReadDir(filepath.Join(p, "sentinel")); len(s) != 0 {
				t.Fatal("sentinel written")
			}
		}
	})
}
