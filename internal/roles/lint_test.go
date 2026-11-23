package roles

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/albertcbrown/ard/internal/scanner"
)

// lintVersion is the golangci-lint version string the golden cases run with.
const lintVersion = "2.15.0"

// runLintNormalize runs normalize.jq the way scan does and returns stdout and
// stderr. mode is findings, baseline or fingerprints.
func runLintNormalize(t *testing.T, dir, mode string) (out, stderr []byte, err error) {
	t.Helper()
	tmp := t.TempDir()
	gl := filepath.Join(tmp, "gl.jsonl")
	if e := os.WriteFile(gl, readFixture(t, filepath.Join(dir, "gl.jsonl"), "/R"), 0o644); e != nil {
		t.Fatal(e)
	}
	flag := "-c"
	if mode != "findings" {
		flag = "-r"
	}
	cmd := exec.Command("jq", flag, "-n", "--arg", "mode", mode, "--arg", "root", "/R", "--arg", "lroot", "/L",
		"--arg", "version", lintVersion, "--rawfile", "baseline", filepath.Join(dir, "baseline"),
		"--slurpfile", "gl", gl, "--slurpfile", "nolint", filepath.Join(dir, "nolint.jsonl"),
		"-f", "scanners/lint/normalize.jq")
	var eb bytes.Buffer
	cmd.Stderr = &eb
	out, err = cmd.Output()
	return out, eb.Bytes(), err
}

// Fixtures in testdata/lint: lintRealCases are regenerated from the tools
// pinned in scanners/lint/Dockerfile by `make lint-scanner-fixtures`
// (testdata/lint/regen.sh, needs Docker). Review every want.jsonl diff by hand:
// the golden test cannot tell a correct change from a regression.
// lintSyntheticCases are hand-written and never regenerated.
var lintRealCases = []string{"basic", "nested_module", "repo_config_new_only"}

var lintSyntheticCases = map[string]string{
	"empty":             "no issues",
	"digits_normalized": "complexity 31 and 32 give one fingerprint",
	"code_prefixes":     "SA4000, G104, govet printf, a lowercase prefix on errcheck that is not split",
	"baseline_filters":  "comments, blank lines, CRLF, a duplicate entry",
	"baseline_mode":     "scan --baseline text",
	"fingerprints_mode": "every pre-baseline fingerprint",
	"bad_baseline_line": "baseline line 3 is not a fingerprint",
	"typecheck":         "typecheck issue fails the run",
	"path_outside_root": "absolute path outside the repository",
	"control_char_path": "newline in a path",
	"long_message":      "512-byte fingerprint, 200-rune title",
	"untrusted_text":    "fences, comment delimiters, mentions, backticks, multi-line text",
	"severity_table":    "each medium rule plus low ones",
	"nolint_directives": "identical lines collapse in one file, two files",
	"many_lines":        "more than 20 occurrences",
}

func lintAllCases() []string {
	out := append([]string(nil), lintRealCases...)
	var syn []string
	for k := range lintSyntheticCases {
		syn = append(syn, k)
	}
	sort.Strings(syn)
	return append(out, syn...)
}

func TestLintFixtureCasesListed(t *testing.T) {
	ents, err := os.ReadDir(filepath.Join("testdata", "lint"))
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
	for _, c := range lintRealCases {
		want[c] = true
	}
	for c := range lintSyntheticCases {
		if want[c] {
			t.Errorf("%s is both real and synthetic", c)
		}
		want[c] = true
	}
	if !reflect.DeepEqual(have, want) {
		t.Errorf("fixture dirs %v != listed cases %v", have, want)
	}
}

func lintMode(t *testing.T, d string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(d, "mode"))
	if err != nil {
		return "findings"
	}
	return strings.TrimSpace(string(b))
}

func TestLintNormalizeGolden(t *testing.T) {
	needTools(t, "jq")
	for _, c := range lintAllCases() {
		t.Run(c, func(t *testing.T) {
			d := filepath.Join("testdata", "lint", c)
			out, stderr, err := runLintNormalize(t, d, lintMode(t, d))
			if wantErr, rerr := os.ReadFile(filepath.Join(d, "want.err")); rerr == nil {
				if err == nil {
					t.Fatalf("jq succeeded, want an error containing %q; out: %s", wantErr, out)
				}
				if len(out) != 0 {
					t.Errorf("stdout not empty on error: %s", out)
				}
				if !strings.Contains(string(stderr), strings.TrimSpace(string(wantErr))) {
					t.Fatalf("stderr %q lacks %q", stderr, wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("jq: %v\n%s", err, stderr)
			}
			name := "want.jsonl"
			if _, serr := os.Stat(filepath.Join(d, "want.txt")); serr == nil {
				name = "want.txt"
			}
			want, rerr := os.ReadFile(filepath.Join(d, name))
			if rerr != nil {
				t.Fatal(rerr)
			}
			if !bytes.Equal(out, want) {
				t.Fatalf("output mismatch\n got: %s\nwant: %s", out, want)
			}
		})
	}
}

func TestLintWantDecodes(t *testing.T) {
	for _, c := range lintAllCases() {
		b, err := os.ReadFile(filepath.Join("testdata", "lint", c, "want.jsonl"))
		if err != nil {
			continue // error and text cases
		}
		fs, err := scanner.DecodeFindings(bytes.NewReader(b))
		if err != nil {
			t.Errorf("%s: %v", c, err)
			continue
		}
		seen := map[string]bool{}
		for _, f := range fs {
			if seen[f.Fingerprint] {
				t.Errorf("%s: duplicate fingerprint %q", c, f.Fingerprint)
			}
			seen[f.Fingerprint] = true
			if n := utf8.RuneCountInString(f.Title); n > scanner.MaxTitleRunes {
				t.Errorf("%s: title has %d runes", c, n)
			}
			if len(f.Body) > 64<<10 {
				t.Errorf("%s: body has %d bytes", c, len(f.Body))
			}
			if len(f.Fingerprint) > 512 {
				t.Errorf("%s: fingerprint has %d bytes", c, len(f.Fingerprint))
			}
		}
	}
}

func TestLintUntrustedText(t *testing.T) {
	needTools(t, "jq")
	d := filepath.Join("testdata", "lint", "untrusted_text")
	out, stderr, err := runLintNormalize(t, d, "findings")
	if err != nil {
		t.Fatalf("jq: %v\n%s", err, stderr)
	}
	fs, err := scanner.DecodeFindings(bytes.NewReader(out))
	if err != nil || len(fs) != 1 {
		t.Fatalf("decode: %v, %d findings", err, len(fs))
	}
	body := fs[0].Body
	if strings.Count(body, "```") != 4 {
		t.Errorf("fences not neutralised: %q", body)
	}
	if strings.Contains(fs[0].Body, "`we") {
		t.Errorf("backtick in path not replaced: %q", body)
	}
}
