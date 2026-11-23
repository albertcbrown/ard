package roles

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func needTools(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s not available", n)
		}
	}
}

func readFixture(t *testing.T, path, root string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.ReplaceAll(b, []byte("@ROOT@"), []byte(root))
}

// runMerge runs merge.jq the way scan does.
func runMerge(t *testing.T, osvJSON, gvJSONL []byte) []byte {
	t.Helper()
	dir := t.TempDir()
	osvPath, gvPath := filepath.Join(dir, "osv.json"), filepath.Join(dir, "gv.jsonl")
	if err := os.WriteFile(osvPath, osvJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gvPath, gvJSONL, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("jq", "-c", "-n", "--arg", "root", "/R", "--arg", "lroot", "/L",
		"--slurpfile", "osv", osvPath, "--slurpfile", "gv", gvPath,
		"-f", "scanners/cve/merge.jq")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("jq: %v\n%s", err, stderr.String())
	}
	return out
}

// Fixtures in testdata/cve: realCases are regenerated from the tools pinned
// in scanners/cve/Dockerfile by `make cve-fixtures` (testdata/cve/regen.sh,
// needs Docker and network). Review every want.jsonl diff by hand: the golden
// test cannot tell a correct change from a regression. syntheticCases are
// hand-written and never regenerated.
var realCases = []string{"both_tools", "osv_only", "not_called", "nested_module", "scoped_npm"}

// syntheticCases are hand-written: real tools can't produce them on demand.
var syntheticCases = map[string]string{
	"empty":              "what scan writes when osv-scanner exits 128",
	"gv_only":            "stdlib-only govulncheck finding; needs an old toolchain",
	"toolchain_switch":   "govulncheck ran with a switched toolchain (wrapper carries toolchain)",
	"symbol_called":      "critical and reachable",
	"severity_fallbacks": "CVSS missing, database severity per level",
	"two_ghsa":           "two GHSA ids for one vulnerability",
	"cve_only":           "no GHSA id",
	"excluded_manifests": "testdata/vendor/node_modules manifests",
	"untrusted_summary":  "hostile advisory summary",
	"hostile_name":       "package name with markup, a backtick and a mention",
}

var cases = allCases()

func allCases() []string {
	out := append([]string(nil), realCases...)
	var syn []string
	for k := range syntheticCases {
		syn = append(syn, k)
	}
	sort.Strings(syn)
	return append(out, syn...)
}

func TestFixtureCasesListed(t *testing.T) {
	ents, err := os.ReadDir(filepath.Join("testdata", "cve"))
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, e := range ents {
		if e.IsDir() {
			have[e.Name()] = true
		}
	}
	want := map[string]bool{}
	for _, c := range realCases {
		want[c] = true
	}
	for c := range syntheticCases {
		if want[c] {
			t.Errorf("%s is both real and synthetic", c)
		}
		want[c] = true
	}
	if !reflect.DeepEqual(have, want) {
		t.Errorf("fixture dirs %v != listed cases %v", have, want)
	}
}

func TestMergeGolden(t *testing.T) {
	needTools(t, "jq")
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			d := filepath.Join("testdata", "cve", c)
			got := runMerge(t, readFixture(t, filepath.Join(d, "osv.json"), "/R"),
				readFixture(t, filepath.Join(d, "gv.jsonl"), "/R"))
			want, err := os.ReadFile(filepath.Join(d, "want.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("output mismatch\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

type finding struct {
	Fingerprint, Title, Severity, Group string
	Files                               []string
	Body                                string
}

func decode(t *testing.T, b []byte) []finding {
	t.Helper()
	var out []finding
	dec := json.NewDecoder(bytes.NewReader(b))
	for dec.More() {
		var f finding
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			t.Fatal(err)
		}
		// Strict, like #23's DecodeFindings: exactly these keys, in this order.
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(raw, &keys); err != nil {
			t.Fatal(err)
		}
		if len(keys) != 6 || !bytes.HasPrefix(raw, []byte(`{"fingerprint":`)) {
			t.Fatalf("unexpected finding shape: %s", raw)
		}
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
	return out
}

func TestMergeFindingShape(t *testing.T) {
	needTools(t, "jq")
	for _, c := range cases {
		d := filepath.Join("testdata", "cve", c)
		out := runMerge(t, readFixture(t, filepath.Join(d, "osv.json"), "/R"),
			readFixture(t, filepath.Join(d, "gv.jsonl"), "/R"))
		prev := ""
		for _, f := range decode(t, out) {
			if f.Fingerprint <= prev {
				t.Errorf("%s: findings not sorted/unique at %s", c, f.Fingerprint)
			}
			prev = f.Fingerprint
			switch f.Severity {
			case "critical", "high", "medium", "low":
			default:
				t.Errorf("%s: bad severity %q", c, f.Severity)
			}
			if len(f.Title) == 0 || len([]rune(f.Title)) > 200 || strings.ContainsAny(f.Title, "\n\r") {
				t.Errorf("%s: bad title %q", c, f.Title)
			}
			if len(f.Body) > 8192 {
				t.Errorf("%s: body too large", c)
			}
		}
	}
}

func TestUntrustedSummaryStaysFenced(t *testing.T) {
	needTools(t, "jq")
	d := filepath.Join("testdata", "cve", "untrusted_summary")
	out := runMerge(t, readFixture(t, filepath.Join(d, "osv.json"), "/R"), nil)
	fs := decode(t, out)
	if len(fs) != 1 {
		t.Fatalf("got %d findings", len(fs))
	}
	body := fs[0].Body
	start := strings.Index(body, "```text\n")
	end := strings.LastIndex(body, "\n```")
	if start < 0 || end < start {
		t.Fatalf("no fence in body: %q", body)
	}
	if strings.Contains(body[start+8:end], "```") {
		t.Fatalf("summary closes its own fence: %q", body)
	}
	for _, bad := range []string{"<!--", "ard:fp"} {
		if strings.Contains(body[:start], bad) {
			t.Fatalf("%q outside the fence: %q", bad, body)
		}
	}
	if strings.Contains(fs[0].Fingerprint+fs[0].Title, "evil summary") {
		t.Fatal("summary leaked into fingerprint or title")
	}
}

// permute applies f to every array in a decoded JSON value, except govulncheck
// traces (real ones have several frames, callee first).
func permute(v any, f func([]any) []any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = permute(e, f)
		}
		return f(out)
	case map[string]any:
		for k, e := range x {
			if k == "trace" { // a call stack: its order is meaning, not noise
				continue
			}
			x[k] = permute(e, f)
		}
		return x
	}
	return v
}

func reverse(a []any) []any {
	for i, j := 0, len(a)-1; i < j; i, j = i+1, j-1 {
		a[i], a[j] = a[j], a[i]
	}
	return a
}

func rotate(a []any) []any {
	if len(a) < 2 {
		return a
	}
	return append(a[1:len(a):len(a)], a[0])
}

func transformJSON(t *testing.T, b []byte, f func([]any) []any) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(permute(v, f))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func transformJSONL(t *testing.T, b []byte, f func([]any) []any) []byte {
	t.Helper()
	var lines []any
	for _, l := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		if len(l) == 0 {
			continue
		}
		var v any
		if err := json.Unmarshal(l, &v); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, v)
	}
	var out bytes.Buffer
	for _, l := range f(permute(lines, f).([]any)) {
		j, _ := json.Marshal(l)
		out.Write(j)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func TestMergeOrderIndependent(t *testing.T) {
	needTools(t, "jq")
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			d := filepath.Join("testdata", "cve", c)
			osvB, gvB := readFixture(t, filepath.Join(d, "osv.json"), "/R"), readFixture(t, filepath.Join(d, "gv.jsonl"), "/R")
			want := runMerge(t, osvB, gvB)
			for name, f := range map[string]func([]any) []any{"reverse": reverse, "rotate": rotate} {
				got := runMerge(t, transformJSON(t, osvB, f), transformJSONL(t, gvB, f))
				if !bytes.Equal(got, want) {
					t.Errorf("%s changed output\n got: %s\nwant: %s", name, got, want)
				}
			}
		})
	}
}

func TestFingerprintIgnoresScoreAndSummary(t *testing.T) {
	needTools(t, "jq")
	d := filepath.Join("testdata", "cve", "both_tools")
	osvB, gvB := readFixture(t, filepath.Join(d, "osv.json"), "/R"), readFixture(t, filepath.Join(d, "gv.jsonl"), "/R")
	var v any
	if err := json.Unmarshal(osvB, &v); err != nil {
		t.Fatal(err)
	}
	// Replace every max_severity and summary.
	var walk func(any)
	walk = func(n any) {
		switch x := n.(type) {
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			if _, ok := x["max_severity"]; ok {
				x["max_severity"] = "2.0"
			}
			if _, ok := x["summary"]; ok {
				x["summary"] = "a completely different summary"
			}
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
	mod, _ := json.Marshal(v)
	byFP := func(fs []finding) map[string]finding {
		m := map[string]finding{}
		for _, f := range fs {
			m[f.Fingerprint] = f
		}
		return m
	}
	a, b := byFP(decode(t, runMerge(t, osvB, gvB))), byFP(decode(t, runMerge(t, mod, gvB)))
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("want the same non-empty finding set, got %d and %d", len(a), len(b))
	}
	for fp, fa := range a {
		fb, ok := b[fp]
		if !ok {
			t.Fatalf("fingerprint %s disappeared", fp)
		}
		if !reflect.DeepEqual(fa.Files, fb.Files) {
			t.Fatalf("%s: files changed", fp)
		}
	}
	const ghsa = "osv:GHSA-69ch-w2m2-3vjp:go.mod:golang.org/x/text"
	if a[ghsa].Severity == b[ghsa].Severity {
		t.Fatalf("%s: severity should have changed with the score (%s)", ghsa, a[ghsa].Severity)
	}
}

// ---- wrapper (scan) tests with fake binaries on PATH ----

const osvShim = `#!/bin/sh
[ "${1:-}" = "--version" ] && { echo "osv-scanner shim"; exit 0; }
echo "osv-scanner $*" >> "$SHIM_LOG"
root=$(pwd -P)
[ -n "${OSV_LOGICAL:-}" ] && root=$PWD
[ -n "${OSV_ROOT:-}" ] && root=$OSV_ROOT
[ -n "${OSV_FIXTURE:-}" ] && sed "s#@ROOT@#$root#g" "$OSV_FIXTURE"
exit "${OSV_RC:-0}"
`

const gvShim = `#!/bin/sh
[ "${1:-}" = "-version" ] && { echo "${GV_SCANNER:-Scanner: govulncheck@v1.10.0}"; exit 0; }
name=$(basename "$(pwd -P)")
echo "govulncheck $name $* tc=$GOTOOLCHAIN path0=${PATH%%:*} self=$0" >> "$SHIM_LOG"
[ "${GV_FAIL_IN:-}" = "$name" ] && { echo boom >&2; exit 1; }
# Mimic Go's read-only module cache.
mkdir -p "$GOMODCACHE/pkg/mod/x" && chmod 555 "$GOMODCACHE/pkg/mod/x"
cat "$GV_DIR/$name"
`

// goShim stands in for go. GO_LIST_FAIL, GO_SEL_FAIL and GO_INSTALL_FAIL make
// the matching call fail. A "go 1.99" in the cwd's go.mod is newer than the
// image's go1.27.4: go list -m fails under GOTOOLCHAIN=local, and
// GOTOOLCHAIN=auto selects go1.99.0. "toolchain go1.99.1" selects that.
const goShim = `#!/bin/sh
echo "go $GOTOOLCHAIN $*" >> "$SHIM_LOG"
newer=
grep -q '^go 1.99' go.mod 2>/dev/null && newer=go1.99.0
grep -q '^toolchain go1.99.1' go.mod 2>/dev/null && newer=go1.99.1
case "$1 $2" in
"list -m")
  [ -n "${GO_LIST_FAIL:-}" ] && { echo "go: list -m failed" >&2; exit 1; }
  if [ "$GOTOOLCHAIN" = local ] && [ "$newer" = go1.99.0 ]; then
    echo "go: go.mod requires go >= 1.99 (running go1.27.4; GOTOOLCHAIN=local)" >&2; exit 1
  fi
  echo example.com/r ;;
"env GOVERSION")
  [ -n "${GO_SEL_FAIL:-}" ] && [ "$GOTOOLCHAIN" = auto ] && { echo "go: download go1.99.0: toolchain not available" >&2; exit 1; }
  if [ "$GOTOOLCHAIN" = auto ] && [ -n "${GO_SEL:-}" ]; then echo "$GO_SEL"
  elif [ "$GOTOOLCHAIN" = auto ] && [ -n "$newer" ]; then echo "$newer"; else echo go1.27.4; fi ;;
"env GOROOT") echo "$FAKE_GOROOT" ;;
install*)
  echo "install CGO_ENABLED=${CGO_ENABLED:-} GOBIN=$GOBIN" >> "$SHIM_LOG"
  [ -n "${GO_INSTALL_FAIL:-}" ] && { echo "go: install failed" >&2; exit 1; }
  mkdir -p "$GOBIN" && cat "$GV_SHIM_SRC" > "$GOBIN/govulncheck" && chmod 755 "$GOBIN/govulncheck" ;;
esac
exit 0
`

// fakeGoroot is $FAKE_GOROOT/bin/go, the go that scan runs first on PATH once
// it has switched toolchains. It reports FAKE_SEL and can install.
const fakeGoroot = `#!/bin/sh
case "$1" in
env) echo "${FAKE_SEL:-go1.99.0}" ;;
install)
  echo "go $GOTOOLCHAIN $*" >> "$SHIM_LOG"
  echo "install CGO_ENABLED=${CGO_ENABLED:-} GOBIN=$GOBIN" >> "$SHIM_LOG"
  [ -n "${GO_INSTALL_FAIL:-}" ] && { echo "go: install failed" >&2; exit 1; }
  mkdir -p "$GOBIN" && cat "$GV_SHIM_SRC" > "$GOBIN/govulncheck" && chmod 755 "$GOBIN/govulncheck" ;;
esac
exit 0
`

type env struct {
	bin, role, repo, tmp, log, gvDir, goroot, gvSrc string
}

// setup builds a hermetic PATH (only the utilities scan needs, plus shims),
// a copy of the role dir from the embedded FS, and an empty repo.
func setup(t *testing.T) *env {
	t.Helper()
	needTools(t, "sh", "jq")
	e := &env{bin: t.TempDir(), role: t.TempDir(), tmp: t.TempDir(), gvDir: t.TempDir()}
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.repo = repo
	e.log = filepath.Join(t.TempDir(), "calls.log")
	e.goroot = t.TempDir()
	e.gvSrc = filepath.Join(t.TempDir(), "govulncheck")
	if err := os.WriteFile(e.gvSrc, []byte(gvShim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.goroot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.goroot, "bin", "go"),
		[]byte(fakeGoroot), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"jq", "find", "sort", "mktemp", "rm", "chmod", "cat", "wc", "tr", "head", "dirname", "sed", "mkdir", "basename", "grep", "tail"} {
		p, err := exec.LookPath(n)
		if err != nil {
			t.Skipf("%s not available", n)
		}
		if err := os.Symlink(p, filepath.Join(e.bin, n)); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"scan", "merge.jq"} {
		b, err := FS.ReadFile("scanners/cve/" + f)
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
	return e
}

func (e *env) shim(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.bin, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (e *env) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(e.repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// govulnFromFixture writes the raw govulncheck output (the messages) for each
// module in a case's wrapped gv.jsonl, keyed by the shim's directory name.
func (e *env) govulnFromFixture(t *testing.T, c string, nameFor map[string]string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "cve", c, "gv.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		if len(l) == 0 {
			continue
		}
		var w struct {
			Manifest string
			Messages []json.RawMessage
		}
		if err := json.Unmarshal(l, &w); err != nil {
			t.Fatal(err)
		}
		var raw bytes.Buffer
		for _, m := range w.Messages {
			raw.Write(m)
			raw.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(e.gvDir, nameFor[w.Manifest]), raw.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *env) run(t *testing.T, extra ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(filepath.Join(e.role, "scan"))
	cmd.Dir = e.repo
	cmd.Env = append([]string{
		"PATH=" + e.bin, "HOME=/nonexistent", "TMPDIR=" + e.tmp,
		"SHIM_LOG=" + e.log, "GV_DIR=" + e.gvDir,
		"FAKE_GOROOT=" + e.goroot, "GV_SHIM_SRC=" + e.gvSrc,
	}, extra...)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return so.String(), se.String(), code
}

func (e *env) calls(t *testing.T) string {
	b, _ := os.ReadFile(e.log)
	return string(b)
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func (e *env) bothToolsRepo(t *testing.T) {
	e.shim(t, "osv-scanner", osvShim)
	e.shim(t, "govulncheck", gvShim)
	e.shim(t, "go", goShim)
	e.write(t, "go.mod", "module example.com/r\n")
	e.govulnFromFixture(t, "both_tools", map[string]string{"go.mod": filepath.Base(e.repo)})
}

func TestScanSuccessMatchesGolden(t *testing.T) {
	for _, rc := range []string{"0", "1"} { // 1 = vulnerabilities found
		t.Run("osv_rc_"+rc, func(t *testing.T) {
			e := setup(t)
			e.bothToolsRepo(t)
			before := listDir(t, e.repo)
			out, errOut, code := e.run(t, "OSV_FIXTURE="+filepath.Join(mustAbs(t, "testdata/cve/both_tools"), "osv.json"), "OSV_RC="+rc)
			if code != 0 {
				t.Fatalf("exit %d\n%s", code, errOut)
			}
			want, _ := os.ReadFile("testdata/cve/both_tools/want.jsonl")
			if out != string(want) {
				t.Fatalf("stdout mismatch\n got: %s\nwant: %s", out, want)
			}
			if !strings.Contains(e.calls(t), "--no-call-analysis=all") || !strings.Contains(e.calls(t), "--format json") {
				t.Errorf("osv-scanner flags not passed: %s", e.calls(t))
			}
			assertClean(t, e, before)
		})
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func assertClean(t *testing.T, e *env, before []string) {
	t.Helper()
	if after := listDir(t, e.repo); !reflect.DeepEqual(before, after) {
		t.Errorf("repo changed: %v -> %v", before, after)
	}
	if left := listDir(t, e.tmp); len(left) != 1 { // only the dir itself
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestScanFailures(t *testing.T) {
	osvFix := filepath.Join(mustAbs(t, "testdata/cve/both_tools"), "osv.json")
	tests := []struct {
		name     string
		prep     func(t *testing.T, e *env)
		extra    []string
		wantCode int
		wantOut  string // exact stdout
		wantErr  string // substring of stderr
		// noInstall: the rebuilt govulncheck must never be installed.
		noInstall bool
	}{
		{name: "osv exits 128 means nothing to scan", prep: func(t *testing.T, e *env) {
			e.shim(t, "osv-scanner", osvShim)
		}, extra: []string{"OSV_RC=128"}},
		{name: "osv exits 127 fails with empty stdout", prep: func(t *testing.T, e *env) { e.bothToolsRepo(t) },
			extra: []string{"OSV_RC=127", "OSV_FIXTURE=" + osvFix}, wantCode: 1},
		{name: "govulncheck fails on the second module", prep: func(t *testing.T, e *env) {
			e.bothToolsRepo(t)
			e.write(t, "sub/go.mod", "module example.com/r/sub\n")
			e.write(t, "ignored/testdata/go.mod", "module example.com/ignored\n")
			if err := os.WriteFile(filepath.Join(e.gvDir, "sub"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}, extra: []string{"OSV_FIXTURE=" + osvFix, "GV_FAIL_IN=sub"}, wantCode: 1},
		{name: "govulncheck missing with go.mod present", prep: func(t *testing.T, e *env) {
			e.shim(t, "osv-scanner", osvShim)
			e.shim(t, "go", "#!/bin/sh\n")
			e.write(t, "go.mod", "module example.com/r\n")
		}, extra: []string{"OSV_FIXTURE=" + osvFix}, wantCode: 2},
		{name: "go missing with go.mod present", prep: func(t *testing.T, e *env) {
			e.shim(t, "osv-scanner", osvShim)
			e.shim(t, "govulncheck", gvShim)
			e.write(t, "go.mod", "module example.com/r\n")
		}, extra: []string{"OSV_FIXTURE=" + osvFix}, wantCode: 2},
		{name: "osv-scanner missing", prep: func(t *testing.T, e *env) {}, wantCode: 2},
		{name: "toolchain selection fails", prep: newerGoRepo, extra: []string{"OSV_FIXTURE=" + osvFix, "GO_SEL_FAIL=1"},
			wantCode: 1, wantErr: "cannot get a Go toolchain for go.mod"},
		{name: "govulncheck build fails", prep: newerGoRepo, extra: []string{"OSV_FIXTURE=" + osvFix, "GO_INSTALL_FAIL=1"},
			wantCode: 1, wantErr: "cannot build govulncheck"},
		{name: "unknown govulncheck version", prep: newerGoRepo,
			extra:    []string{"OSV_FIXTURE=" + osvFix, "GV_SCANNER=Scanner: govulncheck@(devel)"},
			wantCode: 1, wantErr: "unknown govulncheck version", noInstall: true},
		{name: "selected Go version is a path", prep: newerGoRepo, extra: []string{"OSV_FIXTURE=" + osvFix, "GO_SEL=go1/../x"},
			wantCode: 1, wantErr: "unexpected Go version", noInstall: true},
		{name: "govulncheck version with a slash", prep: newerGoRepo,
			extra:    []string{"OSV_FIXTURE=" + osvFix, "GV_SCANNER=Scanner: govulncheck@v1.2/../3"},
			wantCode: 1, wantErr: "unknown govulncheck version", noInstall: true},
		{name: "govulncheck version with one dot", prep: newerGoRepo,
			extra:    []string{"OSV_FIXTURE=" + osvFix, "GV_SCANNER=Scanner: govulncheck@v1.2"},
			wantCode: 1, wantErr: "unknown govulncheck version", noInstall: true},
		{name: "selected GOROOT is another version", prep: newerGoRepo, extra: []string{"OSV_FIXTURE=" + osvFix, "FAKE_SEL=go1.98.0"},
			wantCode: 1, wantErr: "is not go1.99.0", noInstall: true},
		{name: "go list fails without a version problem", prep: func(t *testing.T, e *env) { e.bothToolsRepo(t) },
			extra: []string{"OSV_FIXTURE=" + osvFix, "GO_LIST_FAIL=1"}, wantCode: 1, wantErr: "go list -m failed in go.mod"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			tc.prep(t, e)
			before := listDir(t, e.repo)
			out, errOut, code := e.run(t, tc.extra...)
			if code != tc.wantCode {
				t.Fatalf("exit %d, want %d\n%s", code, tc.wantCode, errOut)
			}
			if out != tc.wantOut {
				t.Fatalf("stdout = %q, want %q", out, tc.wantOut)
			}
			if !strings.Contains(errOut, tc.wantErr) {
				t.Fatalf("stderr lacks %q:\n%s", tc.wantErr, errOut)
			}
			if tc.noInstall && strings.Contains(e.calls(t), " install") {
				t.Fatalf("install was called:\n%s", e.calls(t))
			}
			assertClean(t, e, before)
		})
	}
}

// newerGoRepo is bothToolsRepo with a go.mod whose go directive is newer than
// the image's Go.
func newerGoRepo(t *testing.T, e *env) {
	e.bothToolsRepo(t)
	e.write(t, "go.mod", "module example.com/r\n\ngo 1.99\n")
}

func TestScanToolchainSwitch(t *testing.T) {
	fix := "OSV_FIXTURE=" + filepath.Join(mustAbs(t, "testdata/cve/both_tools"), "osv.json")
	gvLines := func(c string) (n int, lines []string) {
		for _, l := range strings.Split(c, "\n") {
			if strings.HasPrefix(l, "govulncheck ") && strings.Contains(l, "-format json") {
				lines = append(lines, l)
			}
		}
		return len(lines), lines
	}
	installs := func(c string) int { return strings.Count(c, "install golang.org/x/vuln/cmd/govulncheck@v1.10.0\n") }
	tests := []struct {
		name  string
		prep  func(t *testing.T, e *env)
		extra []string
		// switched: how many govulncheck runs use the rebuilt binary.
		switched, runs, installs int
	}{
		{name: "image toolchain suffices", prep: func(t *testing.T, e *env) { e.bothToolsRepo(t) }, runs: 1},
		{name: "toolchain line not newer than the image", prep: func(t *testing.T, e *env) {
			e.bothToolsRepo(t)
			e.write(t, "go.mod", "module example.com/r\n\ngo 1.21\n\ntoolchain go1.27.0\n")
		}, runs: 1},
		{name: "two newer modules share one install", prep: func(t *testing.T, e *env) {
			newerGoRepo(t, e)
			e.write(t, "sub/go.mod", "module example.com/r/sub\n\ngo 1.99\n")
			if err := os.WriteFile(filepath.Join(e.gvDir, "sub"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}, runs: 2, switched: 2, installs: 1},
		{name: "only the newer submodule switches", prep: func(t *testing.T, e *env) {
			e.bothToolsRepo(t)
			e.write(t, "sub/go.mod", "module example.com/r/sub\n\ngo 1.99\n")
			if err := os.WriteFile(filepath.Join(e.gvDir, "sub"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}, runs: 2, switched: 1, installs: 1},
		{name: "toolchain line newer than the image", prep: func(t *testing.T, e *env) {
			e.bothToolsRepo(t)
			e.write(t, "go.mod", "module example.com/r\n\ngo 1.21\n\ntoolchain go1.99.1\n")
		}, extra: []string{"FAKE_SEL=go1.99.1"}, runs: 1, switched: 1, installs: 1},
		{name: "caller GOTOOLCHAIN=auto is overridden", prep: func(t *testing.T, e *env) { e.bothToolsRepo(t) },
			extra: []string{"GOTOOLCHAIN=auto"}, runs: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			tc.prep(t, e)
			before := listDir(t, e.repo)
			out, errOut, code := e.run(t, append([]string{fix}, tc.extra...)...)
			if code != 0 {
				t.Fatalf("exit %d\n%s", code, errOut)
			}
			c := e.calls(t)
			n, lines := gvLines(c)
			if n != tc.runs {
				t.Fatalf("%d govulncheck runs, want %d:\n%s", n, tc.runs, c)
			}
			if got := installs(c); got != tc.installs {
				t.Fatalf("%d installs, want %d:\n%s", got, tc.installs, c)
			}
			sw := 0
			for _, l := range lines {
				if !strings.Contains(l, "tc=local ") {
					t.Errorf("govulncheck did not run with GOTOOLCHAIN=local: %s", l)
				}
				if strings.Contains(l, "path0="+filepath.Join(e.goroot, "bin")+" ") {
					sw++
					if !strings.Contains(l, filepath.Join(e.tmp, "ard-cve.")) || !strings.Contains(l, "/gv/go1.99.") {
						t.Errorf("rebuilt govulncheck not under the temp dir: %s", l)
					}
				}
			}
			if sw != tc.switched {
				t.Fatalf("%d runs on the switched toolchain, want %d:\n%s", sw, tc.switched, c)
			}
			if tc.installs > 0 {
				if !strings.Contains(c, "CGO_ENABLED=0 GOBIN="+e.tmp) {
					t.Errorf("install without CGO_ENABLED=0 under TMPDIR:\n%s", c)
				}
				if !strings.Contains(errOut, "govulncheck: building v1.10.0 with go1.99.") {
					t.Errorf("no build log line:\n%s", errOut)
				}
			} else if strings.Contains(errOut, "building") {
				t.Errorf("unexpected build:\n%s", errOut)
			}
			want, _ := os.ReadFile("testdata/cve/both_tools/want.jsonl")
			got := out
			if tc.switched > 0 {
				// The root module's findings carry the toolchain note.
				if tc.name == "only the newer submodule switches" {
					if strings.Contains(out, "Go toolchain:") {
						t.Errorf("root findings must not carry a toolchain note")
					}
				} else {
					if !strings.Contains(out, "Go toolchain: `go1.99.") {
						t.Errorf("no toolchain note in findings:\n%s", out)
					}
					for _, v := range []string{"go1.99.0", "go1.99.1"} {
						got = strings.ReplaceAll(got, `\nGo toolchain: `+"`"+v+"`"+` (selected from go.mod; standard library findings are measured against it)`, "")
					}
				}
			}
			if got != string(want) {
				t.Fatalf("stdout mismatch\n got: %s\nwant: %s", got, want)
			}
			assertClean(t, e, before)
		})
	}
}

func TestDockerfileTmpdir(t *testing.T) {
	b, err := FS.ReadFile("scanners/cve/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	final := string(b[strings.LastIndex(string(b), "\nFROM "):])
	for _, want := range []string{"\nENV GOTOOLCHAIN=local\n", "\nENV TMPDIR=/var/tmp\n"} {
		if !strings.Contains(final, want) {
			t.Errorf("final stage lacks %q", strings.TrimSpace(want))
		}
	}
}

func TestScanSkipsTestdataModulesAndNoGovulncheck(t *testing.T) {
	e := setup(t)
	e.bothToolsRepo(t)
	e.write(t, "testdata/go.mod", "module example.com/fixture\n")
	e.write(t, "vendor/x/go.mod", "module example.com/x\n")
	if _, _, code := e.run(t, "OSV_FIXTURE="+filepath.Join(mustAbs(t, "testdata/cve/both_tools"), "osv.json")); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if c := e.calls(t); strings.Count(c, "govulncheck ") != 1 || !strings.Contains(c, "-format json ./...") {
		t.Fatalf("expected exactly one govulncheck call, got:\n%s", c)
	}

	// no-govulncheck: never invoked; reachability unknown.
	e = setup(t)
	e.bothToolsRepo(t)
	if err := os.WriteFile(filepath.Join(e.role, "no-govulncheck"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := e.run(t, "OSV_FIXTURE="+filepath.Join(mustAbs(t, "testdata/cve/both_tools"), "osv.json"))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(e.calls(t), "govulncheck ") {
		t.Fatalf("govulncheck ran despite no-govulncheck:\n%s", e.calls(t))
	}
	fs := decode(t, []byte(out))
	if len(fs) == 0 {
		t.Fatalf("no findings: %s", out)
	}
	for _, f := range fs {
		if !strings.Contains(f.Body, "Reachable: unknown") {
			t.Fatalf("want unknown reachability, got %s", out)
		}
	}
}

func TestScanOsvOnlyNoGoModule(t *testing.T) {
	e := setup(t)
	e.shim(t, "osv-scanner", osvShim)
	out, _, code := e.run(t, "OSV_FIXTURE="+filepath.Join(mustAbs(t, "testdata/cve/osv_only"), "osv.json"))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	fs := decode(t, []byte(out))
	if len(fs) == 0 {
		t.Fatalf("no findings: %s", out)
	}
	for _, f := range fs {
		if !strings.Contains(f.Body, "Reachable: unknown") {
			t.Fatalf("want unknown reachability: %s", out)
		}
	}
}

// osv-scanner builds absolute paths from the logical cwd; under a symlinked
// directory they must still be made repo-relative.
func TestScanSymlinkedCwd(t *testing.T) {
	e := setup(t)
	e.bothToolsRepo(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(e.repo, link); err != nil {
		t.Fatal(err)
	}
	fix := filepath.Join(mustAbs(t, "testdata/cve/both_tools"), "osv.json")
	cmd := exec.Command(filepath.Join(e.role, "scan"))
	cmd.Dir = link
	cmd.Env = []string{"PATH=" + e.bin, "HOME=/nonexistent", "TMPDIR=" + e.tmp, "SHIM_LOG=" + e.log,
		"GV_DIR=" + e.gvDir, "OSV_FIXTURE=" + fix, "OSV_LOGICAL=1", "PWD=" + link}
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v\n%s", err, se.String())
	}
	want, _ := os.ReadFile("testdata/cve/both_tools/want.jsonl")
	if so.String() != string(want) {
		t.Fatalf("stdout mismatch (host path leaked?)\n got: %s\nwant: %s", so.String(), want)
	}
}

func TestScanAbsoluteManifestFailsClosed(t *testing.T) {
	e := setup(t)
	e.shim(t, "osv-scanner", osvShim)
	out, errOut, code := e.run(t, "OSV_ROOT=/somewhere/else", "OSV_FIXTURE="+filepath.Join(mustAbs(t, "testdata/cve/osv_only"), "osv.json"))
	if code == 0 || out != "" || !strings.Contains(errOut, "absolute manifest") {
		t.Fatalf("exit %d stdout %q stderr %s", code, out, errOut)
	}
}

// Real fixtures must keep the shape of real tool output: a hand edit of a real
// case is caught here. Update the version with the Dockerfile.
func TestRealFixturesHaveRealShape(t *testing.T) {
	for _, c := range realCases {
		t.Run(c, func(t *testing.T) {
			d := filepath.Join("testdata", "cve", c)
			var osv map[string]json.RawMessage
			if err := json.Unmarshal(readFixture(t, filepath.Join(d, "osv.json"), "/R"), &osv); err != nil {
				t.Fatal(err)
			}
			if _, ok := osv["experimental_config"]; !ok {
				t.Error("osv.json lacks experimental_config")
			}
			gv, err := os.ReadFile(filepath.Join(d, "gv.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range bytes.Split(bytes.TrimSpace(gv), []byte("\n")) {
				if len(l) == 0 {
					continue
				}
				var w struct {
					Messages []struct {
						Config struct {
							ScannerVersion string `json:"scanner_version"`
						} `json:"config"`
					}
				}
				if err := json.Unmarshal(l, &w); err != nil {
					t.Fatal(err)
				}
				if len(w.Messages) == 0 || w.Messages[0].Config.ScannerVersion != "v1.10.0" {
					t.Errorf("first govulncheck message is not a v1.10.0 config: %s", l[:min(len(l), 200)])
				}
			}
		})
	}
}

func TestVerLe(t *testing.T) {
	needTools(t, "sh", "sed")
	src, err := exec.Command("sed", "-n", "/^ver_le()/,/^}/p", "scanners/cve/scan").Output()
	if err != nil || len(src) == 0 {
		t.Fatalf("extract ver_le: %v", err)
	}
	f := filepath.Join(t.TempDir(), "ver_le.sh")
	if err := os.WriteFile(f, src, 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.27.10", "1.27.9", false},
		{"1.27.9", "1.27.10", true},
		{"go1.28.0", "1.28", true},
		{"1.28", "go1.28.0", true},
		{"go1.28rc1", "go1.27.4", false},
		{"go1.27.4", "go1.27.4", true},
	}
	for _, tc := range tests {
		t.Run(tc.a+"_le_"+tc.b, func(t *testing.T) {
			err := exec.Command("sh", "-c", `. "$1"; ver_le "$2" "$3"`, "sh", f, tc.a, tc.b).Run()
			if got := err == nil; got != tc.want {
				t.Fatalf("ver_le %s %s = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
