package roles

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/albertcbrown/ard/internal/scanner"
)

const mtFixtures = "testdata/missing-tests"

// mtBuild builds the helper into a temp dir. It builds from the temp dir, so
// the repo's go.mod is not consulted.
func mtBuild(t *testing.T) string {
	t.Helper()
	needTools(t, "go")
	src, err := filepath.Abs("scanners/missing-tests/missingtests.go")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "missingtests")
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Dir = t.TempDir()
	cmd.Env = append(goEnv(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func mtGo(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = goEnv()
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, se.String())
	}
	return so.Bytes()
}

// mtMeasure runs go list and, unless noCover, go test with coverage in dir. It
// returns the helper's common arguments.
func mtMeasure(t *testing.T, dir string, noCover bool) []string {
	t.Helper()
	aux := t.TempDir()
	pkgs := filepath.Join(aux, "pkgs.json")
	if err := os.WriteFile(pkgs, mtGo(t, dir, "list", "-json", "./..."), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"-module", "example.com/mt", "-prefix", ".", "-packages", pkgs, "-go", "go1.test"}
	if noCover {
		return append(args, "-no-coverage")
	}
	prof := filepath.Join(aux, "cover.out")
	mtGo(t, dir, "test", "-count=1", "-covermode=set", "-coverpkg=./...", "-coverprofile="+prof, "./...")
	return append(args, "-profile", prof)
}

func mtRun(t *testing.T, bin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = goEnv()
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

// mtModule writes files (and a go.mod) to a new temp module.
func mtModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["go.mod"] = "module example.com/mt\n\ngo 1.22\n"
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func mtCases(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir(mtFixtures)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	if len(out) != 7 {
		t.Fatalf("fixture cases: %v", out)
	}
	return out
}

func TestMissingTestsHelper(t *testing.T) {
	bin := mtBuild(t)
	for _, name := range mtCases(t) {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			copyTree(t, filepath.Join(mtFixtures, name), dir)
			_, err := os.Stat(filepath.Join(dir, "no-coverage"))
			args := mtMeasure(t, dir, err == nil)
			if _, err := os.Stat(filepath.Join(dir, "ignore")); err == nil {
				args = append(args, "-ignore", filepath.Join(dir, "ignore"))
			}
			got, se, code := mtRun(t, bin, args...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, se)
			}
			want, err := os.ReadFile(filepath.Join(mtFixtures, name, "want.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("output differs\n--- got\n%s--- want\n%s", got, want)
			}
		})
	}
}

func TestMissingTestsHelperOutputDecodes(t *testing.T) {
	for _, name := range mtCases(t) {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(mtFixtures, name, "want.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			found, err := scanner.DecodeFindings(bytes.NewReader(b))
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
		})
	}
}

func TestMissingTestsFingerprintStable(t *testing.T) {
	bin := mtBuild(t)
	const fp = "missing-tests:go:example.com/mt/p:F"
	// F is never run, then moved to another file with a new body and run by a
	// test that does not name it, then named by a test.
	steps := []struct {
		name  string
		files map[string]string
		sev   string
	}{
		{"uncovered", map[string]string{
			"p/a.go":      "package p\n\nfunc F() int { return 1 }\n",
			"p/a_test.go": "package p\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
		}, "medium"},
		{"moved and run unnamed", map[string]string{
			"p/b.go":      "package p\n\nfunc F() int {\n\tx := 2\n\treturn x + 1\n}\n\nfunc G() int { return F() }\n",
			"p/a_test.go": "package p\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { G() }\n",
		}, "low"},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			dir := mtModule(t, s.files)
			out, se, code := mtRun(t, bin, mtMeasure(t, dir, false)...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, se)
			}
			found, err := scanner.DecodeFindings(strings.NewReader(out))
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range found {
				if f.Fingerprint == fp {
					if string(f.Severity) != s.sev {
						t.Errorf("severity %s, want %s", f.Severity, s.sev)
					}
					return
				}
			}
			t.Errorf("no finding with fingerprint %s in %s", fp, out)
		})
	}
}

func TestMissingTestsHelperLimits(t *testing.T) {
	bin := mtBuild(t)
	long := "X" + strings.Repeat("a", 599)
	dir := mtModule(t, map[string]string{"p/p.go": "package p\n\nfunc " + long + "() int { return 1 }\n\nfunc A() int { return 1 }\n\nfunc B() int { return 1 }\n"})
	args := mtMeasure(t, dir, true)
	t.Run("long symbol", func(t *testing.T) {
		out, se, code := mtRun(t, bin, args...)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, se)
		}
		found, err := scanner.DecodeFindings(strings.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != 3 {
			t.Fatalf("%d findings", len(found))
		}
		hashed := 0
		for _, f := range found {
			if strings.HasPrefix(f.Fingerprint, "missing-tests:go:sha256:") {
				hashed++
			}
			if n := utf8.RuneCountInString(f.Title); n > 200 {
				t.Errorf("title has %d runes", n)
			}
		}
		if hashed != 1 {
			t.Errorf("%d hashed fingerprints, want 1", hashed)
		}
		for _, f := range found {
			if len(f.Title) > 200 && !strings.HasSuffix(f.Title, "…") {
				t.Errorf("cut title %q lacks ellipsis", f.Title)
			}
		}
	})
	t.Run("too many", func(t *testing.T) {
		out, se, code := mtRun(t, bin, append([]string{"-max", "2"}, args...)...)
		if code != 1 || out != "" || !strings.Contains(se, "too many findings") {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, out, se)
		}
	})
}

func TestMissingTestsHelperErrors(t *testing.T) {
	bin := mtBuild(t)
	good := mtModule(t, map[string]string{"p/p.go": "package p\n\nfunc F() int { return 1 }\n", "p/p_test.go": "package p\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { F() }\n"})
	gargs := mtMeasure(t, good, false)
	prof := gargs[len(gargs)-1]
	pkgs := gargs[5]
	aux := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(aux, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	badProf := write("bad.out", "mode: set\nnot a profile line\n")
	badIgnore := write("ignore", "# ok\n\nexample.com/mt/p\nexample.com/mt/p:\n")
	broken := mtModule(t, map[string]string{"p/p.go": "package p\n\nfunc F( {\n"})
	brokenArgs := mtMeasure(t, broken, true)
	base := []string{"-module", "example.com/mt", "-prefix", ".", "-go", "go1.test"}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no packages flag", append(append([]string{}, base...), "-no-coverage"), "-packages"},
		{"profile and no-coverage", append(append([]string{}, base...), "-packages", pkgs, "-profile", prof, "-no-coverage"), "exactly one"},
		{"neither", append(append([]string{}, base...), "-packages", pkgs), "exactly one"},
		{"malformed profile", append(append([]string{}, base...), "-packages", pkgs, "-profile", badProf), "bad.out:2"},
		{"syntax error", brokenArgs, "p.go"},
		{"malformed ignore", append(append([]string{}, base...), "-packages", pkgs, "-profile", prof, "-ignore", badIgnore), "ignore:4"},
		{"missing profile file", append(append([]string{}, base...), "-packages", pkgs, "-profile", filepath.Join(aux, "nope")), "nope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, se, code := mtRun(t, bin, c.args...)
			if code != 1 || out != "" || !strings.HasPrefix(se, "missing-tests: ") || !strings.Contains(se, c.want) {
				t.Fatalf("exit %d, stdout %q, stderr %q (want it to contain %q)", code, out, se, c.want)
			}
		})
	}
}

func TestMissingTestsHelperVets(t *testing.T) {
	needTools(t, "go", "gofmt")
	cmd := exec.Command("go", "vet", "scanners/missing-tests/missingtests.go")
	cmd.Env = goEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
	out, err := exec.Command("gofmt", "-l", "scanners/missing-tests/missingtests.go").CombinedOutput()
	if err != nil || len(out) != 0 {
		t.Fatalf("gofmt -l: %v %s", err, out)
	}
}

func mtScript(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("scanners/missing-tests/scan")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// mtRole copies the role dir and adds the named human-owned files to it.
func mtRole(t *testing.T, extra map[string]string) string {
	t.Helper()
	role := t.TempDir()
	copyTree(t, "scanners/missing-tests", role)
	for name, src := range extra {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(role, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return role
}

func TestMissingTestsScanScript(t *testing.T) {
	gover := strings.TrimSpace(string(mtGo(t, t.TempDir(), "env", "GOVERSION")))
	for _, name := range mtCases(t) {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			copyTree(t, filepath.Join(mtFixtures, name), repo)
			extra := map[string]string{}
			for _, f := range []string{"no-coverage", "ignore"} {
				if _, err := os.Stat(filepath.Join(repo, f)); err == nil {
					extra[f] = filepath.Join(repo, f)
				}
			}
			script := filepath.Join(mtRole(t, extra), "scan")
			want := strings.ReplaceAll(readFile(t, filepath.Join(mtFixtures, name, "want.jsonl")), "go1.test", gover)
			out, se, code := runDocsScript(t, script, repo)
			if code != 0 || out != want {
				t.Fatalf("exit %d\nstdout: %s\nwant: %s\nstderr: %s", code, out, want, se)
			}
			if _, err := scanner.DecodeFindings(strings.NewReader(out)); err != nil {
				t.Errorf("decode: %v", err)
			}
			again, _, _ := runDocsScript(t, script, repo)
			if again != out {
				t.Error("runs differ")
			}
		})
	}
}

func TestMissingTestsScanFailures(t *testing.T) {
	script := mtScript(t)
	t.Run("failing test", func(t *testing.T) {
		repo := mtModule(t, map[string]string{
			"p/p.go":      "package p\n\nfunc F() int { return 1 }\n",
			"p/p_test.go": "package p\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { t.Fatal(\"boom\") }\n",
		})
		out, se, code := runDocsScript(t, script, repo)
		if code != 1 || out != "" || !strings.Contains(se, "go test failed in .") {
			t.Fatalf("exit %d, stdout %q, stderr %s", code, out, se)
		}
	})
	t.Run("build error", func(t *testing.T) {
		repo := mtModule(t, map[string]string{"p/p.go": "package p\n\nfunc F() int { return \"x\" }\n"})
		out, se, code := runDocsScript(t, script, repo)
		if code != 1 || out != "" {
			t.Fatalf("exit %d, stdout %q, stderr %s", code, out, se)
		}
	})
	t.Run("no go", func(t *testing.T) {
		sh, err := exec.LookPath("sh")
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(sh, script)
		cmd.Dir = t.TempDir()
		cmd.Env = []string{"PATH=" + t.TempDir()}
		var se bytes.Buffer
		cmd.Stderr = &se
		err = cmd.Run()
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 2 || !strings.Contains(se.String(), "go not found") {
			t.Fatalf("err %v, stderr %s", err, se.String())
		}
	})
	t.Run("no modules", func(t *testing.T) {
		out, se, code := runDocsScript(t, script, t.TempDir())
		if code != 0 || out != "" {
			t.Fatalf("exit %d, stdout %q, stderr %s", code, out, se)
		}
	})
}

func TestMissingTestsScanNestedModule(t *testing.T) {
	repo := mtModule(t, map[string]string{
		"a.go":       "package mt\n\nfunc Root() int { return 1 }\n",
		"sub/go.mod": "module example.com/sub\n\ngo 1.22\n",
		"sub/s.go":   "package sub\n\nfunc Nested() int { return 1 }\n",
	})
	out, se, code := runDocsScript(t, mtScript(t), repo)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, se)
	}
	found, err := scanner.DecodeFindings(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]bool{}
	for _, f := range found {
		files[f.Files[0]] = true
	}
	if !files["a.go"] || !files["sub/s.go"] {
		t.Errorf("files %v, want a.go and sub/s.go\n%s", files, out)
	}
}
