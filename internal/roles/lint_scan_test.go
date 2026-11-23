package roles

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const stubGolangci = `#!/bin/sh
if [ "$1" = version ]; then echo 2.15.0; exit 0; fi
echo "cwd=$PWD argv=$*" >> "$STUB_DIR/calls.log"
out=
for a in "$@"; do
  case $a in --output.json.path=*) out=${a#--output.json.path=} ;; esac
done
[ -z "$out" ] || cp "$STUB_DIR/report.json" "$out"
[ ! -f "$STUB_DIR/stderr.txt" ] || cat "$STUB_DIR/stderr.txt" >&2
exit "$(cat "$STUB_DIR/rc" 2>/dev/null || echo 0)"
`

const stubGo = `#!/bin/sh
if [ "$1" = env ] && [ "$2" = GOVERSION ]; then echo go1.27.4; exit 0; fi
echo "unexpected go call: $*" >&2
exit 1
`

type lintScanOpts struct {
	baseline    *string // nil: no baseline file
	roleConfig  bool
	noGolangci  bool
	modules     []string // go.mod directories; "." is the root
	issues      []map[string]any
	rc          int
	golangciErr string
	files       map[string]string // extra repo files
	args        []string
}

type lintScanResult struct {
	stdout, stderr string
	code           int
	calls          []string
	repo           string
	role           string
}

func runLintScan(t *testing.T, o lintScanOpts) lintScanResult {
	t.Helper()
	needTools(t, "sh", "jq", "find", "grep")
	jqPath, _ := exec.LookPath("jq")
	jqPath, _ = filepath.EvalSymlinks(jqPath)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	role, repo, stub, tmp := filepath.Join(base, "role"), filepath.Join(base, "repo"), filepath.Join(base, "stub"), filepath.Join(base, "tmp")
	for _, d := range []string{role, repo, stub, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"scan", "normalize.jq"} {
		b, err := os.ReadFile(filepath.Join("scanners", "lint", n))
		if err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(role, n), string(b), 0o755)
	}
	if o.baseline != nil {
		write(filepath.Join(role, "baseline"), *o.baseline, 0o644)
	}
	if o.roleConfig {
		write(filepath.Join(role, "golangci.yml"), "version: \"2\"\n", 0o644)
	}
	if !o.noGolangci {
		write(filepath.Join(stub, "golangci-lint"), stubGolangci, 0o755)
	}
	write(filepath.Join(stub, "go"), stubGo, 0o755)
	if err := os.Symlink(jqPath, filepath.Join(stub, "jq")); err != nil {
		t.Fatal(err)
	}
	for i := range o.issues {
		if fn, _ := o.issues[i]["Pos"].(map[string]any); fn != nil {
			fn["Filename"] = strings.ReplaceAll(fn["Filename"].(string), "@ROOT@", repo)
		}
	}
	rep, _ := json.Marshal(map[string]any{"Issues": o.issues})
	write(filepath.Join(stub, "report.json"), string(rep), 0o644)
	write(filepath.Join(stub, "rc"), fmt.Sprint(o.rc), 0o644)
	if o.golangciErr != "" {
		write(filepath.Join(stub, "stderr.txt"), o.golangciErr, 0o644)
	}
	for _, m := range o.modules {
		write(filepath.Join(repo, m, "go.mod"), "module x\n\ngo 1.21\n", 0o644)
		write(filepath.Join(repo, m, "a.go"), "package x\n", 0o644)
	}
	for p, s := range o.files {
		write(filepath.Join(repo, p), s, 0o644)
	}
	// A read-only worktree, like the executor's.
	if err := exec.Command("chmod", "-R", "a-w", repo).Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("chmod", "-R", "u+w", base).Run() })
	before := listTree(t, repo)

	cmd := exec.Command("sh", append([]string{filepath.Join(role, "scan")}, o.args...)...)
	cmd.Dir = repo
	cmd.Env = []string{"PATH=" + stub + ":/usr/bin:/bin", "HOME=/nonexistent", "TMPDIR=" + tmp, "STUB_DIR=" + stub}
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	if after := listTree(t, repo); after != before {
		t.Errorf("repo changed:\nbefore: %s\nafter: %s", before, after)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("temp dir not cleaned: %v", left)
	}
	var calls []string
	if b, err := os.ReadFile(filepath.Join(stub, "calls.log")); err == nil {
		calls = strings.Split(strings.TrimSpace(string(b)), "\n")
	}
	return lintScanResult{so.String(), se.String(), code, calls, repo, role}
}

func listTree(t *testing.T, root string) string {
	t.Helper()
	var l []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		fi, _ := d.Info()
		l = append(l, fmt.Sprintf("%s %v %d", p, fi.Mode(), fi.Size()))
		return nil
	})
	return strings.Join(l, "\n")
}

func errcheckIssue(line int) map[string]any {
	return map[string]any{"FromLinter": "errcheck", "Text": "Error return value of `f` is not checked",
		"Pos": map[string]any{"Filename": "@ROOT@/a.go", "Line": line}}
}

func strp(s string) *string { return &s }

func TestLintScanStubbed(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		r := runLintScan(t, lintScanOpts{baseline: strp("lint:unused:a.go:gone\n"), modules: []string{"."},
			issues: []map[string]any{errcheckIssue(3), errcheckIssue(4)},
			files:  map[string]string{"b.go": "package x\n\nfunc f() {\n\tg() //nolint:errcheck\n}\n"}})
		if r.code != 0 {
			t.Fatalf("exit %d\n%s", r.code, r.stderr)
		}
		lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
		if len(lines) != 2 || !strings.Contains(lines[0], `"fingerprint":"lint:errcheck:a.go:`) ||
			!strings.Contains(lines[1], `"fingerprint":"lint:nolint:b.go:g() //nolint:errcheck"`) {
			t.Errorf("stdout:\n%s", r.stdout)
		}
		for _, w := range []string{"golangci-lint: 2.15.0", "go: go1.27.4", "golangci-lint: go.mod: 2 issue(s)",
			"lint: 2 finding(s), 0 baselined, 1 baseline entries unused"} {
			if !strings.Contains(r.stderr, w) {
				t.Errorf("stderr lacks %q:\n%s", w, r.stderr)
			}
		}
	})

	t.Run("baselined", func(t *testing.T) {
		r := runLintScan(t, lintScanOpts{baseline: strp("# c\nlint:errcheck:a.go:Error return value of `f` is not checked\n"),
			modules: []string{"."}, issues: []map[string]any{errcheckIssue(3)}})
		if r.code != 0 || r.stdout != "" || !strings.Contains(r.stderr, "1 baselined, 0 baseline entries unused") {
			t.Fatalf("exit %d stdout %q\n%s", r.code, r.stdout, r.stderr)
		}
	})

	t.Run("argv", func(t *testing.T) {
		for _, cfg := range []bool{false, true} {
			r := runLintScan(t, lintScanOpts{baseline: strp(""), roleConfig: cfg, modules: []string{".", "sub", "a/b"}})
			if r.code != 0 || len(r.calls) != 3 {
				t.Fatalf("exit %d, calls %q\n%s", r.code, r.calls, r.stderr)
			}
			// scan sorts the go.mod paths ("./a/b/go.mod" < "./go.mod" < "./sub/go.mod").
			for i, suffix := range []string{"/repo/a/b", "/repo", "/repo/sub"} {
				c := r.calls[i]
				if !strings.Contains(c, "cwd="+r.repo+strings.TrimPrefix(suffix, "/repo")+" ") {
					t.Errorf("call %d runs elsewhere: %s", i, c)
				}
				for _, f := range []string{"run ", "--path-mode=abs", "--output.json.path=", "--output.text.path=stderr", "--show-stats=false",
					"--issues-exit-code=0", "--max-issues-per-linter=0", "--max-same-issues=0", "--new=false", "--new-from-rev= ",
					"--new-from-merge-base= ", "--new-from-patch= ", "--fix=false", "--allow-parallel-runners", " ./..."} {
					if !strings.Contains(c, f) {
						t.Errorf("call %d lacks %q: %s", i, f, c)
					}
				}
				if got := strings.Contains(c, "--config "+r.role+"/golangci.yml "); got != cfg {
					t.Errorf("role config flag = %v, want %v: %s", got, cfg, c)
				}
				if !strings.HasSuffix(strings.TrimSpace(c), "./...") {
					t.Errorf("forced flags must come last: %s", c)
				}
			}
		}
	})

	t.Run("golangci fails", func(t *testing.T) {
		r := runLintScan(t, lintScanOpts{baseline: strp(""), modules: []string{"."}, rc: 3, golangciErr: "boom\n"})
		if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "exit 3") {
			t.Fatalf("exit %d stdout %q\n%s", r.code, r.stdout, r.stderr)
		}
	})

	t.Run("go version error", func(t *testing.T) {
		r := runLintScan(t, lintScanOpts{baseline: strp(""), modules: []string{"sub"}, rc: 3,
			golangciErr: "Error: can't load config: the Go language version (go1.27) used to build golangci-lint is lower than the targeted Go version (1.99)\n"})
		if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "sub/go.mod needs a newer Go") {
			t.Fatalf("exit %d stdout %q\n%s", r.code, r.stdout, r.stderr)
		}
	})

	t.Run("typecheck", func(t *testing.T) {
		is := errcheckIssue(1)
		is["FromLinter"], is["Text"] = "typecheck", "undefined: x"
		r := runLintScan(t, lintScanOpts{baseline: strp(""), modules: []string{"."}, issues: []map[string]any{is, errcheckIssue(2)}})
		if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "typecheck") {
			t.Fatalf("exit %d stdout %q\n%s", r.code, r.stdout, r.stderr)
		}
	})

	t.Run("golangci-lint missing", func(t *testing.T) {
		r := runLintScan(t, lintScanOpts{baseline: strp(""), modules: []string{"."}, noGolangci: true})
		if r.code != 2 || r.stdout != "" {
			t.Fatalf("exit %d stdout %q\n%s", r.code, r.stdout, r.stderr)
		}
	})

	t.Run("no baseline", func(t *testing.T) {
		r := runLintScan(t, lintScanOpts{modules: []string{"."}})
		if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "scan --baseline") {
			t.Fatalf("exit %d stdout %q\n%s", r.code, r.stdout, r.stderr)
		}
	})

	t.Run("bad baseline line", func(t *testing.T) {
		r := runLintScan(t, lintScanOpts{baseline: strp("# ok\nnope\n"), modules: []string{"."}})
		if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "baseline line 2") {
			t.Fatalf("exit %d stdout %q\n%s", r.code, r.stdout, r.stderr)
		}
	})

	t.Run("baseline mode without baseline", func(t *testing.T) {
		r := runLintScan(t, lintScanOpts{modules: []string{"."}, issues: []map[string]any{errcheckIssue(3)}, args: []string{"--baseline"}})
		if r.code != 0 || !strings.Contains(r.stdout, "# lint baseline") ||
			!strings.Contains(r.stdout, "\nlint:errcheck:a.go:Error return value of `f` is not checked\n") {
			t.Fatalf("exit %d stdout %q\n%s", r.code, r.stdout, r.stderr)
		}
	})

	t.Run("baseline mode ignores baseline file", func(t *testing.T) {
		r := runLintScan(t, lintScanOpts{baseline: strp("lint:errcheck:a.go:Error return value of `f` is not checked\n"),
			modules: []string{"."}, issues: []map[string]any{errcheckIssue(3)}, args: []string{"--baseline"}})
		if r.code != 0 || !strings.Contains(r.stdout, "\nlint:errcheck:a.go:") {
			t.Fatalf("exit %d stdout %q\n%s", r.code, r.stdout, r.stderr)
		}
	})

	t.Run("bad argument", func(t *testing.T) {
		for _, args := range [][]string{{"--nope"}, {"--baseline", "x"}} {
			r := runLintScan(t, lintScanOpts{baseline: strp(""), args: args})
			if r.code != 1 || r.stdout != "" {
				t.Fatalf("%v: exit %d stdout %q\n%s", args, r.code, r.stdout, r.stderr)
			}
		}
	})

	t.Run("no go.mod with nolint", func(t *testing.T) {
		r := runLintScan(t, lintScanOpts{baseline: strp(""), files: map[string]string{
			"m.go":              "package m\n\nvar _ = f() // nolint:unused\n",
			"testdata/skip.go":  "//nolint\n",
			"vendor/x/skip.go":  "//nolint\n",
			"node_modules/s.go": "//nolint\n"}})
		if r.code != 0 || len(r.calls) != 0 {
			t.Fatalf("exit %d calls %q\n%s", r.code, r.calls, r.stderr)
		}
		if n := strings.Count(strings.TrimSpace(r.stdout), "\n") + 1; n != 1 || !strings.Contains(r.stdout, `"fingerprint":"lint:nolint:m.go:`) {
			t.Errorf("stdout:\n%s", r.stdout)
		}
	})
}

func TestLintScanIsPosixSh(t *testing.T) {
	needTools(t, "sh")
	if out, err := exec.Command("sh", "-n", filepath.Join("scanners", "lint", "scan")).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
