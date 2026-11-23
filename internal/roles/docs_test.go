package roles

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/albertcbrown/ard/internal/scanner"
)

var (
	docscanOnce sync.Once
	docscanBin  string
	docscanErr  error
	docscanDir  string
)

func goEnv() []string {
	return append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOFLAGS=", "GOWORK=off")
}

// buildDocscan builds scanners/docs/docscan.go once, from a temp dir so the
// repo's go.mod is not consulted, and vets it (go vet ./... skips ignore-tagged
// files).
func buildDocscan(t *testing.T) string {
	t.Helper()
	docscanOnce.Do(func() {
		src, err := filepath.Abs("scanners/docs/docscan.go")
		if err != nil {
			docscanErr = err
			return
		}
		docscanDir, err = os.MkdirTemp("", "docscan-test")
		if err != nil {
			docscanErr = err
			return
		}
		docscanBin = filepath.Join(docscanDir, "docscan")
		for _, args := range [][]string{{"build", "-o", docscanBin, src}, {"vet", src}} {
			cmd := exec.Command("go", args...)
			cmd.Dir = docscanDir
			cmd.Env = append(goEnv(), "GOCACHE="+filepath.Join(docscanDir, "cache"))
			if out, err := cmd.CombinedOutput(); err != nil {
				docscanErr = &exec.ExitError{Stderr: out}
				t.Logf("go %v: %s", args, out)
				return
			}
		}
	})
	if docscanErr != nil {
		t.Fatalf("building docscan: %v", docscanErr)
	}
	return docscanBin
}

func runDocscan(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(buildDocscan(t), args...)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return so.String(), se.String(), code
}

func fixtureArgs(c string) []string {
	args := []string{"-root", filepath.Join("testdata", "docs", c, "src")}
	if ex := filepath.Join("testdata", "docs", c, "exclude"); fileExists(ex) {
		args = append(args, "-exclude", ex)
	}
	return args
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDocscanGolden(t *testing.T) {
	cases, err := os.ReadDir(filepath.Join("testdata", "docs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name(), func(t *testing.T) {
			out, se, code := runDocscan(t, fixtureArgs(c.Name())...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, se)
			}
			want := readFile(t, filepath.Join("testdata", "docs", c.Name(), "want.jsonl"))
			if out != want {
				t.Errorf("stdout differs from want.jsonl:\n got: %s\nwant: %s", out, want)
			}
			if _, err := scanner.DecodeFindings(strings.NewReader(out)); err != nil {
				t.Errorf("DecodeFindings: %v", err)
			}
		})
	}
}

func TestDocscanFailsClosed(t *testing.T) {
	write := func(t *testing.T, root, name, s string) string {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	const bad = "package x\n\nfunc Undoc() {}\n"
	cases := []struct {
		name  string
		setup func(t *testing.T, root string) []string
		want  string
	}{
		{"syntax error", func(t *testing.T, root string) []string {
			write(t, root, "ok/a.go", bad)
			write(t, root, "broken/b.go", "package x\n\nfunc (\n")
			return nil
		}, "broken/b.go"},
		{"bad exclude glob", func(t *testing.T, root string) []string {
			write(t, root, "ok/a.go", bad)
			return []string{"-exclude", write(t, t.TempDir(), "exclude", "[\n")}
		}, "bad pattern"},
		{"unreadable file", func(t *testing.T, root string) []string {
			if os.Getuid() == 0 {
				t.Skip("root can read anything")
			}
			p := write(t, root, "ok/a.go", bad)
			if err := os.Chmod(p, 0); err != nil {
				t.Fatal(err)
			}
			return nil
		}, "ok/a.go"},
		{"control character in path", func(t *testing.T, root string) []string {
			write(t, root, "ok/a\x01b.go", bad)
			return nil
		}, "control character"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			extra := c.setup(t, root)
			out, se, code := runDocscan(t, append([]string{"-root", root}, extra...)...)
			if code == 0 || out != "" || !strings.Contains(se, c.want) {
				t.Fatalf("exit %d, stdout %q, stderr %q; want failure naming %q", code, out, se, c.want)
			}
		})
	}
}

func TestDocscanSkipsSymlinks(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "o.go"), []byte("package o\n\nfunc Leak() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "d", "d.go"), []byte("package d\n\nfunc Leak2() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "o.go"), filepath.Join(root, "link.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "d"), filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	out, se, code := runDocscan(t, "-root", root)
	if code != 0 || out != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q; want no findings", code, out, se)
	}
}

func TestDocscanDeterministic(t *testing.T) {
	args := []string{"-root", filepath.Join("testdata", "docs", "funcs_methods", "src")}
	a, _, _ := runDocscan(t, args...)
	b, _, _ := runDocscan(t, args...)
	if a == "" || a != b {
		t.Fatalf("runs differ or are empty:\n%s\n%s", a, b)
	}
	found, err := scanner.DecodeFindings(strings.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range found {
		if seen[f.Fingerprint] {
			t.Errorf("duplicate fingerprint %s", f.Fingerprint)
		}
		seen[f.Fingerprint] = true
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func runDocsScript(t *testing.T, script, cwd string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command("sh", script)
	cmd.Dir = cwd
	cmd.Env = append(goEnv(), "TMPDIR="+t.TempDir(), "HOME=/nonexistent", "GOCACHE=", "GOMODCACHE=", "GOPATH=")
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return so.String(), se.String(), code
}

func TestDocsScanScript(t *testing.T) {
	script, err := filepath.Abs("scanners/docs/scan")
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	copyTree(t, filepath.Join("testdata", "docs", "funcs_methods", "src"), repo)
	want := readFile(t, filepath.Join("testdata", "docs", "funcs_methods", "want.jsonl"))

	out, se, code := runDocsScript(t, script, repo)
	if code != 0 || out != want {
		t.Fatalf("exit %d\nstdout: %s\nwant: %s\nstderr: %s", code, out, want, se)
	}

	t.Run("exclude file", func(t *testing.T) {
		role := t.TempDir()
		copyTree(t, "scanners/docs", role)
		if err := os.WriteFile(filepath.Join(role, "exclude"), []byte("f/\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, se, code := runDocsScript(t, filepath.Join(role, "scan"), repo)
		if code != 0 || out != "" {
			t.Fatalf("exit %d, stdout %q, stderr %s", code, out, se)
		}
	})

	t.Run("syntax error", func(t *testing.T) {
		bad := t.TempDir()
		copyTree(t, repo, bad)
		if err := os.WriteFile(filepath.Join(bad, "f", "broken.go"), []byte("package f\n\nfunc (\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, se, code := runDocsScript(t, script, bad)
		if code != 1 || out != "" || !strings.Contains(se, "broken.go") {
			t.Fatalf("exit %d, stdout %q, stderr %s", code, out, se)
		}
	})
}

func TestDocsScanOnThisRepo(t *testing.T) {
	a, se, code := runDocscan(t, "-root", filepath.Join("..", ".."))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, se)
	}
	b, _, _ := runDocscan(t, "-root", filepath.Join("..", ".."))
	if a != b {
		t.Error("runs differ")
	}
	found, err := scanner.DecodeFindings(strings.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		for _, p := range f.Files {
			if strings.Contains(p, "testdata/") || strings.HasPrefix(p, ".ard/") {
				t.Errorf("finding %s names skipped path %s", f.Fingerprint, p)
			}
		}
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	if docscanDir != "" {
		os.RemoveAll(docscanDir)
	}
	os.Exit(code)
}
