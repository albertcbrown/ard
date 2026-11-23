package roles

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/scanner"
)

// thFake stands in for trufflehog: it records argv and replays stdout, stderr
// and the exit code from files named by the environment.
const thFake = `#!/bin/sh
if [ "${1:-}" = --version ]; then echo "trufflehog 3.100.0"; exit 0; fi
printf '%s\n' "$*" >> "$TH_ARGV"
[ -z "${TH_STDOUT:-}" ] || cat "$TH_STDOUT"
[ -z "${TH_STDERR:-}" ] || cat "$TH_STDERR" >&2
exit "${TH_RC:-0}"
`

type thResult struct {
	stdout, stderr string
	code           int
	argv           string
	tmp            string
}

type thLine map[string]any

func thResultLine(raw, det, file string, line int) thLine {
	return thLine{"DetectorName": det, "Raw": raw, "RawV2": "v2-marker", "Redacted": "redacted-marker",
		"SecretParts": []string{"parts-marker"}, "ExtraData": map[string]any{"k": "extra-marker"},
		"StructuredData": map[string]any{"k": "structured-marker"}, "VerificationError": "verr-marker",
		"repository":     "repo-marker",
		"SourceMetadata": map[string]any{"Data": map[string]any{"Filesystem": map[string]any{"file": file, "line": line}}}}
}

func thSummary(chunks, unverified int) string {
	b, _ := json.Marshal(map[string]any{"level": "info", "msg": "finished scanning", "chunks": chunks, "verified_secrets": 0, "unverified_secrets": unverified})
	return string(b)
}

func thJoin(lines []thLine) string {
	var sb strings.Builder
	for _, l := range lines {
		b, _ := json.Marshal(l)
		sb.Write(b)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// thRun runs the shipped scan in a temp repo with a fake trufflehog.
func thRun(t *testing.T, out, errlog string, rc int, files map[string]string, extra map[string]string) thResult {
	t.Helper()
	return thRunFull(t, mustAbs(t, "scanners/trufflehog"), out, errlog, rc, files, extra)
}

func thRunWith(t *testing.T, role, out, errlog string, rc int) thResult {
	t.Helper()
	return thRunFull(t, role, out, errlog, rc, map[string]string{"a.txt": "x"}, nil)
}

func thRunFull(t *testing.T, role, out, errlog string, rc int, files map[string]string, extra map[string]string) thResult {
	t.Helper()
	needTools(t, "sh", "jq")
	if _, err := exec.LookPath("sha256sum"); err != nil {
		needTools(t, "shasum")
	}
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	bin := filepath.Join(work, "bin")
	tmp := filepath.Join(work, "tmp")
	for _, d := range []string{repo, bin, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "trufflehog"), []byte(thFake), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(repo, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"stdout": out, "stderr": errlog} {
		if err := os.WriteFile(filepath.Join(work, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", filepath.Join(role, "scan"))
	cmd.Dir = repo
	cmd.Env = []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "TMPDIR=" + tmp, "TH_ARGV=" + filepath.Join(work, "argv"),
		"TH_STDOUT=" + filepath.Join(work, "stdout"), "TH_STDERR=" + filepath.Join(work, "stderr"), "TH_RC=" + string(rune('0'+rc))}
	for k, v := range extra {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	argv, _ := os.ReadFile(filepath.Join(work, "argv"))
	return thResult{so.String(), se.String(), code, string(argv), tmp}
}

func thTmpEmpty(t *testing.T, tmp string) {
	t.Helper()
	if ents, _ := os.ReadDir(tmp); len(ents) != 0 {
		t.Errorf("temp dir not cleaned: %v", ents)
	}
}

func TestTrufflehogAggregatesAndFingerprints(t *testing.T) {
	in := thJoin([]thLine{
		thResultLine("canary-raw-alpha", "Zeta", "sub/b.txt", 3),
		thResultLine("canary-raw-alpha", "Alpha", "a.txt", 1),
		thResultLine("canary-raw-alpha", "Alpha", "./a.txt", 1),
		thResultLine("canary-raw-beta", "Alpha", "a.txt", 2),
	})
	r := thRun(t, in, thSummary(2, 4), 0, map[string]string{"a.txt": "x"}, nil)
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	fs, err := scanner.DecodeFindings(strings.NewReader(r.stdout))
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 {
		t.Fatalf("findings = %d, want 2:\n%s", len(fs), r.stdout)
	}
	got := map[string]scanner.Finding{}
	for _, f := range fs {
		got[f.Fingerprint] = f
	}
	alpha, ok := got["trufflehog:d4bf4d1967fad7b7"]
	if !ok || got["trufflehog:ce658c895bb3b307"].Fingerprint == "" {
		t.Fatalf("golden fingerprints missing:\n%s", r.stdout)
	}
	if alpha.Title != "Possible Alpha credential in a.txt" || alpha.Severity != "high" || strings.Join(alpha.Files, ",") != "a.txt,sub/b.txt" {
		t.Errorf("alpha = %+v", alpha)
	}
	for _, s := range []string{"Do not add `ard:pending`", "1. Rotate", "6. Git history"} {
		if !strings.Contains(alpha.Body, s) {
			t.Errorf("body lacks %q", s)
		}
	}
	for _, leak := range []string{"canary-raw", "v2-marker", "redacted-marker", "parts-marker", "extra-marker", "structured-marker", "verr-marker", "repo-marker"} {
		if strings.Contains(r.stdout+r.stderr, leak) {
			t.Errorf("%q leaked", leak)
		}
	}
	thTmpEmpty(t, r.tmp)
	// deterministic: reversed input gives identical stdout
	lines := strings.Split(strings.TrimSpace(in), "\n")
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	r2 := thRun(t, strings.Join(lines, "\n")+"\n", thSummary(2, 4), 0, map[string]string{"a.txt": "x"}, nil)
	if r2.stdout != r.stdout {
		t.Errorf("output depends on input order")
	}
}

func TestTrufflehogFailsClosed(t *testing.T) {
	ok := thResultLine("canary-raw-alpha", "Alpha", "a.txt", 1)
	bad := func(mut func(thLine)) string {
		l := thResultLine("canary-raw-alpha", "Alpha", "a.txt", 1)
		mut(l)
		return thJoin([]thLine{l})
	}
	fsData := func(l thLine, file any) {
		l["SourceMetadata"] = map[string]any{"Data": map[string]any{"Filesystem": map[string]any{"file": file}}}
	}
	cases := []struct {
		name, out, log string
		rc             int
		allow          string
		want           string
	}{
		{"exit1", thJoin([]thLine{ok}), thSummary(1, 1), 1, "", "failed (exit status 1)"},
		{"exit2", thJoin([]thLine{ok}), thSummary(1, 1), 2, "", "failed (exit status 2)"},
		{"no summary", thJoin([]thLine{ok}), "", 0, "", "finished scanning"},
		{"zero chunks", "", thSummary(0, 0), 0, "", "scanned nothing"},
		{"count mismatch", thJoin([]thLine{ok}), thSummary(1, 2), 0, "", "does not match"},
		{"malformed", "{not json\n", thSummary(1, 1), 0, "", "cannot parse"},
		{"non-object", "[1]\n", thSummary(1, 1), 0, "", "cannot parse"},
		{"absolute path", bad(func(l thLine) { fsData(l, "/etc/x") }), thSummary(1, 1), 0, "", "cannot parse"},
		{"dotdot", bad(func(l thLine) { fsData(l, "../x") }), thSummary(1, 1), 0, "", "cannot parse"},
		{"inner dotdot", bad(func(l thLine) { fsData(l, "a/../b") }), thSummary(1, 1), 0, "", "cannot parse"},
		{"control char", bad(func(l thLine) { fsData(l, "a\nb") }), thSummary(1, 1), 0, "", "cannot parse"},
		{"git source", bad(func(l thLine) { l["SourceMetadata"] = map[string]any{"Data": map[string]any{"Git": map[string]any{}}} }), thSummary(1, 1), 0, "", "cannot parse"},
		{"no raw", bad(func(l thLine) { delete(l, "Raw"); delete(l, "RawV2") }), thSummary(1, 1), 0, "", "cannot parse"},
		{"bad detector", bad(func(l thLine) { l["DetectorName"] = "bad name!" }), thSummary(1, 1), 0, "", "cannot parse"},
		{"bad allow", thJoin([]thLine{ok}), thSummary(1, 1), 0, "nonsense\n", "allow:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			needTools(t, "sh", "jq")
			role := mustAbs(t, "scanners/trufflehog")
			if c.allow != "" { // allow lives beside scan: use a copy of the role dir
				cp := t.TempDir()
				for _, n := range []string{"scan", "findings.jq", "exclude-paths"} {
					b, _ := os.ReadFile(filepath.Join(role, n))
					os.WriteFile(filepath.Join(cp, n), b, 0o755)
				}
				os.WriteFile(filepath.Join(cp, "allow"), []byte(c.allow), 0o644)
				role = cp
			}
			r := thRunRole(t, role, c.out, c.log, c.rc)
			if r.code == 0 || r.stdout != "" {
				t.Fatalf("exit %d, stdout %q; want failure with empty stdout", r.code, r.stdout)
			}
			if !strings.Contains(r.stderr, c.want) {
				t.Errorf("stderr lacks %q:\n%s", c.want, r.stderr)
			}
			if strings.Contains(r.stderr, "canary-raw") {
				t.Errorf("stderr leaks the secret:\n%s", r.stderr)
			}
			thTmpEmpty(t, r.tmp)
		})
	}
}

// thRunRole is thRun against another role directory (used for allow files).
func thRunRole(t *testing.T, role, out, errlog string, rc int) thResult {
	t.Helper()
	return thRunWith(t, role, out, errlog, rc)
}

func TestTrufflehogArgvAndExcludes(t *testing.T) {
	r := thRun(t, "", thSummary(0, 0), 0, nil, nil)
	if r.code != 0 || r.stdout != "" {
		t.Fatalf("empty repo: exit %d stdout %q stderr %s", r.code, r.stdout, r.stderr)
	}
	argv := strings.TrimSpace(r.argv)
	if !strings.HasPrefix(argv, "filesystem . --json --no-update --no-color --no-verification --no-ignore-tag --fail-on-scan-errors --concurrency=2 -x ") {
		t.Errorf("argv = %q", argv)
	}
	for _, f := range []string{"--since-commit", "--max-depth", "--detector-timeout", "--archive-timeout", "--filter-unverified", "--filter-entropy", "--results", "--include-detectors", "--exclude-detectors", "--json-legacy"} {
		if strings.Contains(argv, f) {
			t.Errorf("forbidden flag %s in argv", f)
		}
	}
	thTmpEmpty(t, r.tmp)
}

func TestTrufflehogMissingTool(t *testing.T) {
	needTools(t, "sh")
	cmd := exec.Command("/bin/sh", filepath.Join(mustAbs(t, "scanners/trufflehog"), "scan"))
	cmd.Env = []string{"PATH=" + t.TempDir()}
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 2 || so.Len() != 0 || !strings.Contains(se.String(), "trufflehog not found") {
		t.Fatalf("err %v stdout %q stderr %q", err, so.String(), se.String())
	}
}

func TestTrufflehogDockerfilePins(t *testing.T) {
	th, _ := os.ReadFile("scanners/trufflehog/Dockerfile")
	cve, _ := os.ReadFile("scanners/cve/Dockerfile")
	pin := func(b []byte) string {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "ARG GO_IMAGE=") {
				return l
			}
		}
		return ""
	}
	if pin(th) == "" || pin(th) != pin(cve) {
		t.Errorf("GO_IMAGE pin differs from cve's")
	}
	for _, s := range []string{"ENV TMPDIR=/var/tmp", "ARG TRUFFLEHOG_VERSION=", "sha256sum -c"} {
		if !strings.Contains(string(th), s) {
			t.Errorf("Dockerfile lacks %q", s)
		}
	}
	if strings.Contains(string(th), "COPY scan") || strings.Contains(string(th), "findings.jq /") {
		t.Errorf("Dockerfile must not copy scan files")
	}
}

func TestTrufflehogAllow(t *testing.T) {
	in := thJoin([]thLine{
		thResultLine("canary-raw-alpha", "Alpha", "a.txt", 1),
		thResultLine("canary-raw-beta", "Alpha", "a.txt", 2),
	})
	role := mustAbs(t, "scanners/trufflehog")
	cp := t.TempDir()
	for _, n := range []string{"scan", "findings.jq", "exclude-paths"} {
		b, _ := os.ReadFile(filepath.Join(role, n))
		if err := os.WriteFile(filepath.Join(cp, n), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// no allow file: both findings kept
	r := thRunWith(t, cp, in, thSummary(2, 2), 0)
	if r.code != 0 || strings.Count(r.stdout, "\n") != 2 {
		t.Fatalf("no allow: exit %d, stdout:\n%s\n%s", r.code, r.stdout, r.stderr)
	}
	allow := "# known fixture\n\n  trufflehog:d4bf4d1967fad7b7  # alpha\n"
	if err := os.WriteFile(filepath.Join(cp, "allow"), []byte(allow), 0o644); err != nil {
		t.Fatal(err)
	}
	r = thRunWith(t, cp, in, thSummary(2, 2), 0)
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	fs, err := scanner.DecodeFindings(strings.NewReader(r.stdout))
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Fingerprint != "trufflehog:ce658c895bb3b307" {
		t.Fatalf("allowed fingerprint not dropped:\n%s", r.stdout)
	}
	thTmpEmpty(t, r.tmp)
}
