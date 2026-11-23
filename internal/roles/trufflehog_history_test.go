package roles

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const thHist = "scanners/trufflehog-history"

func thGitLine(raw, det, file string, line int) thLine {
	l := thResultLine(raw, det, file, line)
	l["SourceMetadata"] = map[string]any{"Data": map[string]any{"Git": map[string]any{
		"file": file, "line": line, "commit": "commit-marker", "email": "email-marker", "timestamp": "ts-marker", "repository": "repo-marker"}}}
	return l
}

func thHistRun(t *testing.T, out, errlog string, rc int, extra map[string]string) thResult {
	t.Helper()
	if extra == nil {
		extra = map[string]string{"ARD_HISTORY": t.TempDir()}
	}
	return thRunFull(t, mustAbs(t, thHist), out, errlog, rc, map[string]string{"a.txt": "x"}, extra)
}

func TestTrufflehogHistoryFingerprintMatchesHead(t *testing.T) {
	head := thRun(t, thJoin([]thLine{thResultLine("canary-raw-alpha", "Alpha", "a.txt", 1)}), thSummary(1, 1), 0, map[string]string{"a.txt": "x"}, nil)
	hist := thHistRun(t, thJoin([]thLine{thGitLine("canary-raw-alpha", "Alpha", "old/b.txt", 7)}), thSummary(1, 1), 0, nil)
	if head.code != 0 || hist.code != 0 {
		t.Fatalf("exit %d/%d: %s %s", head.code, hist.code, head.stderr, hist.stderr)
	}
	re := regexp.MustCompile(`"fingerprint":"(trufflehog:[0-9a-f]{16})"`)
	a, b := re.FindStringSubmatch(head.stdout), re.FindStringSubmatch(hist.stdout)
	if a == nil || b == nil || a[1] != b[1] {
		t.Fatalf("fingerprints differ: %v vs %v", a, b)
	}
	if !strings.Contains(hist.stdout, "old/b.txt") || !strings.Contains(hist.stdout, "line 7") {
		t.Errorf("location missing: %s", hist.stdout)
	}
	if strings.Contains(hist.stdout, "closes itself on the next run") {
		t.Errorf("history body must not promise autoclose")
	}
	thTmpEmpty(t, hist.tmp)
}

func TestTrufflehogHistoryNoLeak(t *testing.T) {
	in := thJoin([]thLine{thGitLine("canary-raw-alpha", "Alpha", "a.txt", 1), thGitLine("canary-raw-alpha", "Alpha", "a.txt", 2)})
	r := thHistRun(t, in, thSummary(2, 2), 0, nil)
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if n := strings.Count(r.stdout, "\n"); n != 1 {
		t.Errorf("want one finding for one value, got %d", n)
	}
	for _, m := range []string{"canary-raw", "v2-marker", "redacted-marker", "parts-marker", "extra-marker", "structured-marker", "verr-marker", "commit-marker", "email-marker", "ts-marker", "repo-marker"} {
		if strings.Contains(r.stdout, m) || strings.Contains(r.stderr, m) {
			t.Errorf("marker %q leaked", m)
		}
	}
	thTmpEmpty(t, r.tmp)
}

func TestTrufflehogHistoryFailsClosed(t *testing.T) {
	good := thJoin([]thLine{thGitLine("canary-raw-alpha", "Alpha", "a.txt", 1)})
	noGit := thResultLine("canary-raw-alpha", "Alpha", "a.txt", 1)
	for name, c := range map[string]struct {
		out, err string
		rc       int
	}{
		"exit":       {good, thSummary(1, 1), 1},
		"no summary": {good, "", 0},
		"mismatch":   {good, thSummary(1, 2), 0},
		"no git":     {thJoin([]thLine{noGit}), thSummary(1, 1), 0},
		"bad path":   {thJoin([]thLine{thGitLine("canary-raw-alpha", "Alpha", "../x", 1)}), thSummary(1, 1), 0},
	} {
		r := thHistRun(t, c.out, c.err, c.rc, nil)
		if r.code != 1 || r.stdout != "" {
			t.Errorf("%s: exit %d stdout %q", name, r.code, r.stdout)
		}
		thTmpEmpty(t, r.tmp)
	}
}

func TestTrufflehogHistoryArgv(t *testing.T) {
	hist := t.TempDir()
	r := thHistRun(t, "", thSummary(0, 0), 0, map[string]string{"ARD_HISTORY": hist})
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	argv := strings.TrimSpace(r.argv)
	if !strings.HasPrefix(argv, "git file://"+hist+" --json --no-update --no-color --no-verification --no-ignore-tag --fail-on-scan-errors --concurrency=2 -x ") {
		t.Errorf("argv = %q", argv)
	}
	for _, f := range []string{"--since-commit", "--max-depth", "--branch", "--filter-", "--results", "--detector-timeout", "--archive-timeout"} {
		if strings.Contains(argv, f) {
			t.Errorf("forbidden flag %s", f)
		}
	}
}

func TestTrufflehogHistoryEnvAndTools(t *testing.T) {
	// Missing ARD_HISTORY and a non-directory both exit 2.
	for _, extra := range []map[string]string{{"ARD_HISTORY": ""}, {"ARD_HISTORY": "/nonexistent-history"}} {
		r := thHistRun(t, "", thSummary(0, 0), 0, extra)
		if r.code != 2 || r.stdout != "" {
			t.Errorf("%v: exit %d stderr %s", extra, r.code, r.stderr)
		}
	}
	needTools(t, "sh")
	cmd := exec.Command("/bin/sh", filepath.Join(mustAbs(t, thHist), "scan"))
	cmd.Env = []string{"PATH=" + t.TempDir()}
	if err := cmd.Run(); err == nil || err.(*exec.ExitError).ExitCode() != 2 {
		t.Errorf("missing tool: %v", err)
	}
}

func TestTrufflehogHistoryZeroChunksWithCommits(t *testing.T) {
	needTools(t, "git")
	hist := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", hist).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	env := append(os.Environ(), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@b", "GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@b")
	run := func(args ...string) string {
		c := exec.Command("git", append([]string{"--git-dir", hist}, args...)...)
		c.Env = env
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	c := run("commit-tree", "-m", "m", "4b825dc642cb6eb9a060e54bf8d69288fbee4904")
	run("update-ref", "refs/heads/main", c)
	r := thHistRun(t, "", thSummary(0, 0), 0, map[string]string{"ARD_HISTORY": hist})
	if r.code != 1 || r.stdout != "" {
		t.Errorf("zero chunks with commits: exit %d stdout %q stderr %s", r.code, r.stdout, r.stderr)
	}
	r = thHistRun(t, "", thSummary(0, 0), 0, nil) // not a repository: empty history is fine
	if r.code != 0 {
		t.Errorf("empty history: exit %d: %s", r.code, r.stderr)
	}
}

func TestTrufflehogHistoryDockerfileAndSalt(t *testing.T) {
	a, _ := os.ReadFile("scanners/trufflehog/Dockerfile")
	b, _ := os.ReadFile(thHist + "/Dockerfile")
	pins := func(s []byte) (o []string) {
		for _, l := range strings.Split(string(s), "\n") {
			if strings.HasPrefix(l, "ARG ") || strings.HasPrefix(l, "ENV ") {
				o = append(o, l)
			}
		}
		return
	}
	if strings.Join(pins(a), "\n") != strings.Join(pins(b), "\n") {
		t.Errorf("Dockerfile pins differ from trufflehog's")
	}
	hashLine := func(p string) string {
		s, _ := os.ReadFile(p)
		for _, l := range strings.Split(string(s), "\n") {
			if strings.Contains(l, "ard-trufflehog-v1") {
				return l
			}
		}
		return ""
	}
	if h := hashLine("scanners/trufflehog/scan"); h == "" || h != hashLine(thHist+"/scan") {
		t.Errorf("hash line differs between scanners")
	}
	ex1, _ := os.ReadFile("scanners/trufflehog/exclude-paths")
	ex2, _ := os.ReadFile(thHist + "/exclude-paths")
	if string(ex1) != string(ex2) {
		t.Errorf("exclude-paths differ")
	}
}
