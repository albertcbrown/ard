package roles

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

var cases = []string{
	"both_tools", "osv_only", "gv_only", "module_level_only", "symbol_called",
	"severity_fallbacks", "two_ghsa", "cve_only", "excluded_manifests",
	"nested_module", "untrusted_summary", "scoped_npm", "empty",
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

// permute applies f to every array in a decoded JSON value.
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
	a, b := decode(t, runMerge(t, osvB, gvB)), decode(t, runMerge(t, mod, gvB))
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("want one finding each, got %d and %d", len(a), len(b))
	}
	if a[0].Fingerprint != b[0].Fingerprint {
		t.Fatalf("fingerprint changed: %q -> %q", a[0].Fingerprint, b[0].Fingerprint)
	}
	if a[0].Severity == b[0].Severity {
		t.Fatalf("severity should have changed with the score")
	}
	if !reflect.DeepEqual(a[0].Files, b[0].Files) {
		t.Fatal("files changed")
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
[ "${1:-}" = "-version" ] && { echo "govulncheck shim"; exit 0; }
name=$(basename "$(pwd -P)")
echo "govulncheck $name $*" >> "$SHIM_LOG"
[ "${GV_FAIL_IN:-}" = "$name" ] && { echo boom >&2; exit 1; }
# Mimic Go's read-only module cache.
mkdir -p "$GOMODCACHE/pkg/mod/x" && chmod 555 "$GOMODCACHE/pkg/mod/x"
cat "$GV_DIR/$name"
`

type env struct {
	bin, role, repo, tmp, log, gvDir string
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
	for _, n := range []string{"jq", "find", "sort", "mktemp", "rm", "chmod", "cat", "wc", "tr", "head", "dirname", "sed", "mkdir", "basename"} {
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
	e.shim(t, "go", "#!/bin/sh\necho go shim\n")
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
		calls    func(c string) bool
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
			assertClean(t, e, before)
		})
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
	if len(fs) != 1 || !strings.Contains(fs[0].Body, "Reachable: unknown") {
		t.Fatalf("want one finding with unknown reachability, got %s", out)
	}
}

func TestScanOsvOnlyNoGoModule(t *testing.T) {
	e := setup(t)
	e.shim(t, "osv-scanner", osvShim)
	out, _, code := e.run(t, "OSV_FIXTURE="+filepath.Join(mustAbs(t, "testdata/cve/osv_only"), "osv.json"))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if fs := decode(t, []byte(out)); len(fs) != 1 || !strings.Contains(fs[0].Body, "Reachable: unknown") {
		t.Fatalf("got %s", out)
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
