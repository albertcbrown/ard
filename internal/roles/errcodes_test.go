package roles

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/scanner"
)

type ecEnv struct{ bin, role, tmp, repo string }

func ecSetup(t *testing.T, codes string) *ecEnv {
	t.Helper()
	needTools(t, "sh", "jq")
	e := &ecEnv{bin: t.TempDir(), role: t.TempDir(), tmp: t.TempDir()}
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.repo = repo
	for _, n := range []string{"jq", "find", "awk", "grep", "xargs", "sort", "mktemp", "rm", "cat", "tr", "dirname", "env"} {
		p, err := exec.LookPath(n)
		if err != nil {
			t.Skipf("%s not available", n)
		}
		if err := os.Symlink(p, filepath.Join(e.bin, n)); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"scan", "errcodes.jq"} {
		b, err := FS.ReadFile("scanners/error-codes/" + f)
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if f == "scan" {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(e.role, f), b, mode); err != nil {
			t.Fatal(err)
		}
	}
	if codes != "" {
		if err := os.WriteFile(filepath.Join(e.role, "codes.json"), []byte(codes), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *ecEnv) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(e.repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *ecEnv) runIn(t *testing.T, cwd string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(filepath.Join(e.role, "scan"))
	cmd.Dir = cwd
	cmd.Env = []string{"PATH=" + e.bin, "HOME=/nonexistent", "TMPDIR=" + e.tmp}
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

func (e *ecEnv) run(t *testing.T) (string, string, int) { return e.runIn(t, e.repo) }

const baseCodes = `{"pattern":"E[0-9]{4}","registry":["reg.txt"],"entry":"^E[0-9]{4} =","docs":["docs/errors.md"]}`

// ecRepo has E1001 (registered, documented) and E1002 (registered, documented).
func (e *ecEnv) cleanRepo(t *testing.T) {
	e.write(t, "reg.txt", "E1001 = a\nE1002 = b\n")
	e.write(t, "docs/errors.md", "E1001 and E1002\n")
	e.write(t, "main.go", "use(E1001)\n")
}

// summary is "fingerprint severity group files" per finding.
func summary(t *testing.T, out string) []string {
	t.Helper()
	fs, err := scanner.DecodeFindings(strings.NewReader(out))
	if err != nil {
		t.Fatalf("stdout is not valid findings: %v\n%s", err, out)
	}
	var s []string
	for _, f := range fs {
		s = append(s, f.Fingerprint+" "+string(f.Severity)+" "+f.Group+" "+strings.Join(f.Files, ","))
	}
	return s
}

func TestScan(t *testing.T) {
	type tc struct {
		name  string
		codes string // "-" = no codes.json
		setup func(t *testing.T, e *ecEnv)
		want  []string
	}
	clean := func(t *testing.T, e *ecEnv) { e.cleanRepo(t) }
	cases := []tc{
		{"clean", baseCodes, clean, nil},
		{"unregistered", baseCodes, func(t *testing.T, e *ecEnv) {
			e.cleanRepo(t)
			e.write(t, "b.go", "E7777\n")
			e.write(t, "a.go", "E7777 E7777\n")
		}, []string{"error-codes:unregistered:E7777 medium unregistered codes a.go,b.go"}},
		{"duplicate in one file", baseCodes, func(t *testing.T, e *ecEnv) {
			e.cleanRepo(t)
			e.write(t, "reg.txt", "E1001 = a\nE1002 = b\nE1001 = c\n")
		}, []string{"error-codes:duplicated:E1001 high duplicate codes reg.txt"}},
		{"duplicate across files", `{"pattern":"E[0-9]{4}","registry":["r/*.txt"]}`, func(t *testing.T, e *ecEnv) {
			e.write(t, "r/a.txt", "E1001\n")
			e.write(t, "r/b.txt", "E1001\n")
		}, []string{"error-codes:duplicated:E1001 high duplicate codes r/a.txt,r/b.txt"}},
		{"same code twice on one registry line", `{"pattern":"E[0-9]{4}","registry":["reg.txt"]}`, func(t *testing.T, e *ecEnv) {
			e.write(t, "reg.txt", "E1001 E1001\n")
		}, nil},
		{"entry filters definitions", baseCodes, func(t *testing.T, e *ecEnv) {
			e.cleanRepo(t)
			e.write(t, "reg.txt", "E1001 = a\nE1002 = b\n# see E1003\n")
		}, nil},
		{"undocumented", baseCodes, func(t *testing.T, e *ecEnv) {
			e.cleanRepo(t)
			e.write(t, "docs/errors.md", "E1001\n")
		}, []string{"error-codes:undocumented:E1002 low undocumented codes reg.txt"}},
		{"docs unset", `{"pattern":"E[0-9]{4}","registry":["reg.txt"]}`, func(t *testing.T, e *ecEnv) {
			e.write(t, "reg.txt", "E1001\n")
		}, nil},
		{"word boundaries", baseCodes, func(t *testing.T, e *ecEnv) {
			e.cleanRepo(t)
			e.write(t, "b.go", "XE1001 E10011 E1001_x aE7001 E7002b\n")
		}, nil},
		{"ignored directories", baseCodes, func(t *testing.T, e *ecEnv) {
			e.cleanRepo(t)
			for _, p := range []string{".ard/12/issue.md", "vendor/x.go", "a/vendor/x.go", "testdata/x.go", "node_modules/x.js", ".git/x", "sub/.git/x"} {
				e.write(t, p, "E7777\n")
			}
		}, nil},
		{"sources and exclude", `{"pattern":"E[0-9]{4}","registry":["reg.txt"],"sources":["*.go"],"exclude":["*_test.go","gen/","x/**/skip.go"]}`, func(t *testing.T, e *ecEnv) {
			e.write(t, "reg.txt", "E1001\n")
			e.write(t, "a.txt", "E7001\n")
			e.write(t, "a_test.go", "E7002\n")
			e.write(t, "gen/a.go", "E7003\n")
			e.write(t, "x/y/z/skip.go", "E7004\n")
			e.write(t, "x/y/keep.go", "E7005\n")
		}, []string{"error-codes:unregistered:E7005 medium unregistered codes x/y/keep.go"}},
		{"registry and docs are not uses", baseCodes, func(t *testing.T, e *ecEnv) {
			e.write(t, "reg.txt", "E1001 = a\n")
			e.write(t, "docs/errors.md", "E1001 E5555\n")
		}, nil},
		{"symlink to outside file", baseCodes, func(t *testing.T, e *ecEnv) {
			e.cleanRepo(t)
			out := filepath.Join(t.TempDir(), "o.txt")
			if err := os.WriteFile(out, []byte("E7777\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(out, filepath.Join(e.repo, "link.go")); err != nil {
				t.Fatal(err)
			}
		}, nil},
		{"binary, big and long lines", baseCodes, func(t *testing.T, e *ecEnv) {
			e.cleanRepo(t)
			e.write(t, "bin.dat", "E7001\x00\n")
			e.write(t, "big.txt", strings.Repeat("x", 1<<20+4096)+"\nE7002\n")
			e.write(t, "long.txt", "E7003 "+strings.Repeat("y", 17000)+"\nE7004 ok\n")
		}, []string{"error-codes:unregistered:E7004 medium unregistered codes long.txt"}},
		{"disallowed code characters", `{"pattern":"E[0-9]{4}[^ ]*","registry":["reg.txt"]}`, func(t *testing.T, e *ecEnv) {
			e.write(t, "reg.txt", "E1001\n")
			e.write(t, "a.go", "E7001@bob E7002`x "+"E7003"+strings.Repeat("a", 130)+" E1001\n")
		}, nil},
		{"hostile path", baseCodes, func(t *testing.T, e *ecEnv) {
			e.cleanRepo(t)
			e.write(t, "a`<!--x-->.go", "E7777\n")
		}, []string{"error-codes:unregistered:E7777 medium unregistered codes a`<!--x-->.go"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := ecSetup(t, c.codes)
			c.setup(t, e)
			out, errOut, code := e.run(t)
			if code != 0 {
				t.Fatalf("exit %d\n%s", code, errOut)
			}
			got := summary(t, out)
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Fatalf("findings\n got: %q\nwant: %q\nstderr: %s", got, c.want, errOut)
			}
			switch c.name {
			case "binary, big and long lines":
				if !strings.Contains(errOut, "skipped 1 long lines") {
					t.Errorf("long line not counted: %s", errOut)
				}
			case "disallowed code characters":
				if !strings.Contains(errOut, "dropped 3 match") {
					t.Errorf("dropped codes not counted: %s", errOut)
				}
			case "hostile path":
				fs, _ := scanner.DecodeFindings(strings.NewReader(out))
				if i := strings.Index(fs[0].Body, "```text"); i < 0 || strings.Contains(fs[0].Body[:i], "<!--") || strings.Count(fs[0].Body, "```") != 2 || strings.Contains(fs[0].Body, "<!--") {
					t.Errorf("body not fenced safely: %q", fs[0].Body)
				}
			}
		})
	}
}

func TestScanNotConfigured(t *testing.T) {
	e := ecSetup(t, "")
	e.write(t, "a.go", "E1001\n")
	out, errOut, code := e.run(t)
	if code != 0 || out != "" || !strings.Contains(errOut, "not configured") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestErrCodesScanFailures(t *testing.T) {
	good := func(t *testing.T, e *ecEnv) { e.cleanRepo(t) }
	cases := []struct {
		name, codes, msg string
		setup            func(t *testing.T, e *ecEnv)
	}{
		{"bad json", `{`, "", good},
		{"unknown key", `{"pattern":"E1","registry":["reg.txt"],"nope":1}`, "unknown key nope", good},
		{"wrong type", `{"pattern":"E1","registry":"reg.txt"}`, "registry", good},
		{"wrong docs type", `{"pattern":"E1","registry":["reg.txt"],"docs":[1]}`, "docs", good},
		{"empty pattern", `{"pattern":"","registry":["reg.txt"]}`, "pattern", good},
		{"empty-matching pattern", `{"pattern":"E*","registry":["reg.txt"]}`, "empty string", good},
		{"bad regex", `{"pattern":"E(","registry":["reg.txt"]}`, "does not compile", good},
		{"bad entry regex", `{"pattern":"E1","registry":["reg.txt"],"entry":"("}`, "does not compile", good},
		{"registry matches nothing", `{"pattern":"E[0-9]{4}","registry":["gone.txt"]}`, "registry matches no file", good},
		{"docs match nothing", `{"pattern":"E[0-9]{4}","registry":["reg.txt"],"docs":["gone.md"]}`, "docs matches no file", good},
		{"file in both", `{"pattern":"E[0-9]{4}","registry":["reg.txt"],"docs":["*.txt"]}`, "both registry and docs", good},
		{"too many findings", `{"pattern":"E[0-9]{4}","registry":["reg.txt"]}`, "5000", func(t *testing.T, e *ecEnv) {
			e.write(t, "reg.txt", "E0000\n")
			var u strings.Builder
			for i := 1; i <= 5001; i++ {
				fmt.Fprintf(&u, "E%04d\n", i)
			}
			e.write(t, "uses.go", u.String())
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := ecSetup(t, c.codes)
			c.setup(t, e)
			out, errOut, code := e.run(t)
			if code != 1 || out != "" {
				t.Fatalf("code=%d out=%q err=%s", code, out, errOut)
			}
			if !strings.Contains(errOut, c.msg) {
				t.Fatalf("stderr %q lacks %q", errOut, c.msg)
			}
		})
	}
}

func TestScanDeterministic(t *testing.T) {
	files := map[string]string{"reg.txt": "E1001 = a\nE1001 = b\n", "a.go": "E7001\n", "b.go": "E7002 E7001\n"}
	order := [][]string{{"reg.txt", "a.go", "b.go"}, {"b.go", "reg.txt", "a.go"}}
	var outs []string
	for _, o := range order {
		e := ecSetup(t, `{"pattern":"E[0-9]{4}","registry":["reg.txt"]}`)
		for _, f := range o {
			e.write(t, f, files[f])
		}
		out, errOut, code := e.run(t)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		outs = append(outs, out)
	}
	if outs[0] != outs[1] || outs[0] == "" {
		t.Fatalf("output differs by creation order:\n%s\n%s", outs[0], outs[1])
	}
	// A use moved to another file keeps every fingerprint.
	e := ecSetup(t, `{"pattern":"E[0-9]{4}","registry":["reg.txt"]}`)
	e.write(t, "reg.txt", files["reg.txt"])
	e.write(t, "c.go", "E7001\n")
	e.write(t, "d.go", "E7002 E7001\n")
	moved, _, _ := e.run(t)
	fp := func(s string) []string {
		var r []string
		for _, l := range summary(t, s) {
			r = append(r, strings.Fields(l)[0])
		}
		return r
	}
	if strings.Join(fp(moved), ",") != strings.Join(fp(outs[0]), ",") {
		t.Fatalf("fingerprints changed when uses moved: %v vs %v", fp(moved), fp(outs[0]))
	}
}

func TestScanSymlinkedCwdErrCodes(t *testing.T) {
	e := ecSetup(t, baseCodes)
	e.cleanRepo(t)
	e.write(t, "b.go", "E7777\n")
	want, _, code := e.run(t)
	if code != 0 || want == "" {
		t.Fatalf("code=%d out=%q", code, want)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(e.repo, link); err != nil {
		t.Fatal(err)
	}
	got, _, code := e.runIn(t, link)
	if code != 0 || got != want {
		t.Fatalf("code=%d\n got: %s\nwant: %s", code, got, want)
	}
}

func TestErrCodesJQGlob(t *testing.T) {
	needTools(t, "jq")
	cases := []struct {
		glob, path string
		want       bool
	}{
		{"*.go", "a.go", true}, {"*.go", "x/y/a.go", true}, {"*.go", "a.gox", false},
		{"*_test.go", "x/a_test.go", true},
		{"examples/", "examples/a.go", true}, {"examples/", "x/examples/a.go", false}, {"examples/", "examples", false},
		{"internal/*/codes.go", "internal/e/codes.go", true}, {"internal/*/codes.go", "internal/a/b/codes.go", false},
		{"internal/**/codes.go", "internal/a/b/codes.go", true},
		{"a?.txt", "ab.txt", true}, {"a?.txt", "a/.txt", false},
		{"a.b/c+d", "a.b/c+d", true}, {"a.b/c+d", "aXb/c+d", false},
	}
	for _, c := range cases {
		t.Run(c.glob+" "+c.path, func(t *testing.T) {
			dir := t.TempDir()
			js := `{"pattern":"E1","registry":["` + c.glob + `"]}`
			if err := os.WriteFile(filepath.Join(dir, "c.json"), []byte(js), 0o644); err != nil {
				t.Fatal(err)
			}
			prog, _ := filepath.Abs("scanners/error-codes/errcodes.jq")
			st, err := exec.Command("jq", "-n", "--arg", "mode", "settings", "--slurpfile", "s", filepath.Join(dir, "c.json"), "-f", prog).Output()
			if err != nil {
				t.Fatal(err)
			}
			sp := filepath.Join(dir, "s.json")
			os.WriteFile(sp, st, 0o644)
			cmd := exec.Command("jq", "-R", "-r", "--arg", "mode", "classify", "--arg", "class", "r", "--slurpfile", "s", sp, "-f", prog)
			cmd.Stdin = strings.NewReader("./" + c.path + "\n")
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(out)) != ""; got != c.want {
				t.Fatalf("glob %q on %q = %v, want %v", c.glob, c.path, got, c.want)
			}
		})
	}
}

func TestErrorCodesDockerfilePinned(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	ec, cve := read("scanners/error-codes/Dockerfile"), read("scanners/cve/Dockerfile")
	for _, re := range []string{`(?m)^ARG GO_IMAGE=\S+@sha256:[0-9a-f]{64}$`, `(?m)^ARG JQ_VERSION=.*$`, `(?m)^ARG JQ_SHA256_AMD64=[0-9a-f]{64}$`, `(?m)^ARG JQ_SHA256_ARM64=[0-9a-f]{64}$`} {
		r := regexp.MustCompile(re)
		a, b := r.FindString(ec), r.FindString(cve)
		if a == "" || a != b {
			t.Errorf("%s: error-codes %q, cve %q", re, a, b)
		}
	}
}
