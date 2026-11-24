//go:build integration

package roles

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/scanner"
)

// thiPinnedVersion reads ARG TRUFFLEHOG_VERSION from the role's Dockerfile.
func thiPinnedVersion(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("scanners/trufflehog/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^ARG TRUFFLEHOG_VERSION=(\S+)$`).FindSubmatch(b)
	if m == nil {
		t.Fatal("no ARG TRUFFLEHOG_VERSION= line in scanners/trufflehog/Dockerfile")
	}
	return string(m[1])
}

// thiBin returns ARD_TRUFFLEHOG_BIN. Unset skips; set at any other version
// than the role Dockerfile's pin fails, never skips: the symlink and Raw
// behaviour under test is version-specific. Get the binary from the release
// tarball for the host OS at the pinned version, checked against that
// release's trufflehog_<version>_checksums.txt.
func thiBin(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("ARD_TRUFFLEHOG_BIN")
	if bin == "" {
		t.Skip("ARD_TRUFFLEHOG_BIN not set")
	}
	pin := thiPinnedVersion(t)
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("%s --version: %v", bin, err)
	}
	if !regexp.MustCompile(`(^|[^0-9.])` + regexp.QuoteMeta(pin) + `([^0-9.]|$)`).Match(out) {
		t.Fatalf("ARD_TRUFFLEHOG_BIN reports %q, want version %s (the role Dockerfile's pin)", strings.TrimSpace(string(out)), pin)
	}
	return bin
}

func thiRand(t *testing.T, alphabet string, n int) string {
	t.Helper()
	b := make([]byte, n)
	for i := range b {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			t.Fatal(err)
		}
		b[i] = alphabet[j.Int64()]
	}
	return string(b)
}

const (
	thiUpper  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	thiB64    = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	thiAlnum  = "abcdefghijklmnopqrstuvwxyz0123456789"
	thiHexLow = "0123456789abcdef"
)

// thiAWS returns a random AWS-shaped key id and secret, built at run time so
// no credential-shaped literal is in the source.
func thiAWS(t *testing.T) (id, secret string) {
	return "AK" + "IA" + thiRand(t, thiUpper, 16), thiRand(t, thiB64, 40)
}

type thiPlanted struct {
	a, b, c, d, e []string // the values of each planted secret
}

func (p thiPlanted) all() []string {
	var out []string
	for _, g := range [][]string{p.a, p.b, p.c, p.d, p.e} {
		out = append(out, g...)
	}
	return out
}

// thiPlantRepo builds the temp repo: A in two files, B a credentialed URI, C
// behind an ignore tag (must still be reported), D under an excluded path, E
// outside the tree behind a symlink. The tree is read-only, as in the executor.
func thiPlantRepo(t *testing.T) (string, thiPlanted) {
	t.Helper()
	repo, outside := t.TempDir(), t.TempDir()
	var p thiPlanted
	aid, asec := thiAWS(t)
	cid, csec := thiAWS(t)
	did, dsec := thiAWS(t)
	eid, esec := thiAWS(t)
	// Over 57 bytes, so a base64 that wraps lines would change the fingerprint.
	pw := thiRand(t, thiAlnum, 24)
	uri := "https://" + thiRand(t, thiAlnum, 10) + ":" + pw + "@" + thiRand(t, thiAlnum, 12) + ".example.com/" + thiRand(t, thiAlnum, 20)
	p.a, p.b, p.c, p.d, p.e = []string{aid, asec}, []string{pw, uri}, []string{cid, csec}, []string{did, dsec}, []string{eid, esec}
	pair := func(id, sec, tail string) string {
		return fmt.Sprintf("v1=%s\nv2=%s%s\n", id, sec, tail)
	}
	files := map[string]string{
		"a/one.env":         pair(aid, asec, ""),
		"b/two.env":         pair(aid, asec, ""),
		"c/three.txt":       "v3 = " + uri + "\n",
		"d/four.txt":        fmt.Sprintf("v1=%s v2=%s # trufflehog:ignore\n", cid, csec),
		".ard/7/logs/x.log": pair(did, dsec, ""),
		"README.md":         "nothing here\n",
	}
	for name, s := range files {
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(outside, "secret.env")
	if err := os.WriteFile(target, []byte(pair(eid, esec, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "e"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(repo, "e/link.env")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("chmod", "-R", "u+w", repo).Run() })
	if out, err := exec.Command("chmod", "-R", "a-w", repo).CombinedOutput(); err != nil {
		t.Fatalf("chmod: %v %s", err, out)
	}
	return repo, p
}

// thiLeaks reports which planted values (or their base64) occur in s, as
// indexes only: a failure message must never carry a value.
func thiLeaks(p thiPlanted, s []byte) []int {
	var hits []int
	for i, v := range p.all() {
		if bytes.Contains(s, []byte(v)) || bytes.Contains(s, []byte(base64.StdEncoding.EncodeToString([]byte(v)))) {
			hits = append(hits, i)
		}
	}
	return hits
}

func thiLeaksInDir(t *testing.T, p thiPlanted, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if b, err := os.ReadFile(path); err == nil {
			if h := thiLeaks(p, b); len(h) > 0 {
				t.Errorf("planted value(s) %v left in %s", h, path)
			}
		}
		return nil
	})
}

func thiFingerprint(raw string) string {
	sum := sha256.Sum256([]byte("ard-trufflehog-v1\x00" + base64.StdEncoding.EncodeToString([]byte(raw))))
	return "trufflehog:" + hex.EncodeToString(sum[:])[:16]
}

// thiOracle runs trufflehog directly with the scan's flags and returns the
// fingerprints it implies. The raw values stay in memory.
func thiOracle(t *testing.T, bin, repo, home, tmp string) []string {
	t.Helper()
	role, _ := filepath.Abs("scanners/trufflehog")
	cmd := exec.Command(bin, "filesystem", ".", "--json", "--no-update", "--no-verification", "--no-ignore-tag",
		"-x", filepath.Join(role, "exclude-paths"))
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "HOME="+home, "TMPDIR="+tmp)
	var so bytes.Buffer
	cmd.Stdout = &so
	if err := cmd.Run(); err != nil {
		t.Fatalf("oracle trufflehog: %v", err)
	}
	set := map[string]bool{}
	dec := json.NewDecoder(&so)
	for dec.More() {
		var r struct{ Raw, RawV2 string }
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("oracle decode: %v", err)
		}
		raw := r.Raw
		if raw == "" {
			raw = r.RawV2
		}
		set[thiFingerprint(raw)] = true
	}
	var out []string
	for fp := range set {
		out = append(out, fp)
	}
	sort.Strings(out)
	return out
}

// thiCheckFindings asserts the structure shared by the host and Docker tests
// and returns the fingerprints.
func thiCheckFindings(t *testing.T, stdout []byte) []string {
	t.Helper()
	fs, err := scanner.DecodeFindings(bytes.NewReader(stdout))
	if err != nil {
		t.Fatalf("DecodeFindings: %v", err)
	}
	if len(decode(t, stdout)) != len(fs) {
		t.Errorf("decode and DecodeFindings disagree")
	}
	if len(fs) != 3 {
		t.Errorf("got %d findings, want 3 (A, B, C)", len(fs))
	}
	var fps []string
	var aFiles bool
	for _, f := range fs {
		fps = append(fps, f.Fingerprint)
		if !regexp.MustCompile(`^trufflehog:[0-9a-f]{16}$`).MatchString(f.Fingerprint) {
			t.Errorf("bad fingerprint %q", f.Fingerprint)
		}
		if f.Severity != "high" || f.Group != "" {
			t.Errorf("%s: severity %q group %q, want high and empty", f.Fingerprint, f.Severity, f.Group)
		}
		for _, file := range f.Files {
			if strings.HasPrefix(file, ".ard/7/logs/") || file == "e/link.env" {
				t.Errorf("%s lists %s, which must not be scanned", f.Fingerprint, file)
			}
		}
		if reflect.DeepEqual(f.Files, []string{"a/one.env", "b/two.env"}) {
			aFiles = true
		}
	}
	if !aFiles {
		t.Errorf("no finding lists exactly a/one.env and b/two.env")
	}
	sort.Strings(fps)
	return fps
}

// thiHostEnv returns an environment running the real scan with the given
// binary first on PATH and fresh HOME and TMPDIR.
func thiHostEnv(t *testing.T, bin string) (env []string, home, tmp string) {
	t.Helper()
	abs, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	bd := t.TempDir()
	if err := os.Symlink(abs, filepath.Join(bd, "trufflehog")); err != nil {
		t.Fatal(err)
	}
	home, tmp = t.TempDir(), t.TempDir()
	return append(os.Environ(), "PATH="+bd+string(os.PathListSeparator)+os.Getenv("PATH"), "HOME="+home, "TMPDIR="+tmp), home, tmp
}

func thiRunScan(t *testing.T, repo string, env []string) (stdout, stderr []byte, code int) {
	t.Helper()
	scan, _ := filepath.Abs("scanners/trufflehog/scan")
	cmd := exec.Command(scan)
	cmd.Dir, cmd.Env = repo, env
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return so.Bytes(), se.Bytes(), code
}

// TestTrufflehogReal runs the real role against the real binary (at the
// pinned version) on a run-time repo.
func TestTrufflehogReal(t *testing.T) {
	bin := thiBin(t)
	needTools(t, "jq", "base64")
	if _, err := exec.LookPath("sha256sum"); err != nil {
		needTools(t, "shasum")
	}
	repo, p := thiPlantRepo(t)
	env, home, tmp := thiHostEnv(t, bin)

	so, se, code := thiRunScan(t, repo, env)
	if code != 0 {
		t.Fatalf("exit %d\nstderr:\n%s", code, se)
	}
	fps := thiCheckFindings(t, so)
	if want := thiOracle(t, bin, repo, t.TempDir(), t.TempDir()); !reflect.DeepEqual(fps, want) {
		t.Errorf("fingerprints %v, oracle %v", fps, want)
	}
	if h := thiLeaks(p, so); len(h) > 0 {
		t.Errorf("planted value(s) %v in stdout", h)
	}
	if h := thiLeaks(p, se); len(h) > 0 {
		t.Errorf("planted value(s) %v in stderr", h)
	}
	thiLeaksInDir(t, p, tmp)
	thiLeaksInDir(t, p, home)
	if got, err := redact.Builtin().Scan(t.Context(), bytes.NewReader(so)); err != nil || len(got) > 0 {
		t.Errorf("redact.Builtin on stdout: %d findings, err %v", len(got), err)
	}
	so2, _, code2 := thiRunScan(t, repo, env)
	if code2 != 0 || !bytes.Equal(so, so2) {
		t.Errorf("second run differs (exit %d)", code2)
	}
}
