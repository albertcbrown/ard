package tarball

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func write(t *testing.T, p, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, mode)
}

func listTar(t *testing.T, data []byte) map[string]*tar.Header {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]*tar.Header{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = h
	}
}

func lstatView(t *testing.T, dir string) map[string]string {
	t.Helper()
	v := map[string]string{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		fi, _ := d.Info()
		rel, _ := filepath.Rel(dir, p)
		s := fi.Mode().String()
		if fi.Mode()&fs.ModeSymlink != 0 {
			l, _ := os.Readlink(p)
			s += " -> " + l
		} else if fi.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			s += " " + string(b)
		}
		v[rel] = s
		return nil
	})
	return v
}

func TestRoundTrip(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "a/b/file"), "hello", 0o644)
	write(t, filepath.Join(src, "run.sh"), "#!/bin/sh", 0o755)
	os.MkdirAll(filepath.Join(src, "empty"), 0o755)
	os.Symlink("a/b/file", filepath.Join(src, "rel"))
	os.Symlink("nowhere", filepath.Join(src, "dangling"))

	var buf bytes.Buffer
	st, err := Pack(&buf, []Root{{Name: "work", Dir: src}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Skipped != 0 || st.Bytes != int64(len("hello")+len("#!/bin/sh")) {
		t.Fatalf("stats = %+v", st)
	}
	var again bytes.Buffer
	Pack(&again, []Root{{Name: "work", Dir: src}})
	if !bytes.Equal(buf.Bytes(), again.Bytes()) {
		t.Error("pack is not deterministic")
	}

	_, dest, _ := setup(t)
	if _, err := Unpack(&buf, dest, DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	got, want := lstatView(t, filepath.Join(dest, "work")), lstatView(t, src)
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
}

func TestPackFilesRoot(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "stdout"), "out", 0o644)
	write(t, filepath.Join(src, "other"), "no", 0o644)
	var buf bytes.Buffer
	if _, err := Pack(&buf, []Root{{Name: "out", Dir: src, Files: []string{"stdout", "missing"}}}); err != nil {
		t.Fatal(err)
	}
	got := listTar(t, buf.Bytes())
	if len(got) != 2 || got["out/stdout"] == nil || got["out/"] == nil {
		t.Fatalf("entries = %v", got)
	}
}

func TestPackSkips(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs")
	}
	src := t.TempDir()
	write(t, filepath.Join(src, ".git"), "gitdir: x", 0o644)
	write(t, filepath.Join(src, "sub/.GIT/config"), "x", 0o644)
	write(t, filepath.Join(src, "keep"), "x", 0o644)
	if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	st, err := Pack(&buf, []Root{{Name: "w", Dir: src}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Skipped != 1 {
		t.Errorf("Skipped = %d", st.Skipped)
	}
	for name := range listTar(t, buf.Bytes()) {
		if strings.Contains(strings.ToLower(name), ".git") || strings.Contains(name, "pipe") {
			t.Errorf("unexpected entry %s", name)
		}
	}
}

func TestPackSymlinkToSecret(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	write(t, secret, "SECRET-MARKER", 0o600)
	src := t.TempDir()
	os.Symlink(secret, filepath.Join(src, "leak"))
	var buf bytes.Buffer
	if _, err := Pack(&buf, []Root{{Name: "w", Dir: src}}); err != nil {
		t.Fatal(err)
	}
	h := listTar(t, buf.Bytes())["w/leak"]
	if h == nil || h.Typeflag != tar.TypeSymlink || h.Linkname != secret {
		t.Fatalf("header = %+v", h)
	}
	gz, _ := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	raw, _ := io.ReadAll(gz)
	if bytes.Contains(raw, []byte("SECRET-MARKER")) {
		t.Fatal("secret contents packed")
	}
}

func TestPackRootName(t *testing.T) {
	for _, n := range []string{"", "a/b", "..", ".", ".git", ".GIT"} {
		if _, err := Pack(io.Discard, []Root{{Name: n, Dir: t.TempDir()}}); err == nil {
			t.Errorf("name %q accepted", n)
		}
	}
}
