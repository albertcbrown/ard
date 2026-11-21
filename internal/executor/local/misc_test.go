//go:build unix

package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
)

func TestLayout(t *testing.T) {
	l, _ := New(Options{Root: "/r"})
	if got := l.WorktreePath("o/r", "J1"); got != "/r/work/o-r/J1" {
		t.Error(got)
	}
	if got := l.logDir("o/r", 0); got != "/r/logs/o-r/noissue" {
		t.Error(got)
	}
	ts := time.Date(2026, 11, 21, 10, 15, 0, 0, time.UTC)
	if got := LogBase("worker", "dev", ts); got != "worker-dev-20261121T101500Z" {
		t.Error(got)
	}
}

func TestTail(t *testing.T) {
	big := strings.Repeat("x", 100) + "\n"
	tests := []struct {
		name, in string
		n        int
		want     string
	}{
		{"fewer lines than n", "a\nb\n", 5, "a\nb\n"},
		{"last one", "a\nb\nc\n", 1, "c\n"},
		{"no trailing newline", "a\nb\nc", 2, "b\nc"},
		{"empty", "", 3, ""},
		{"crosses chunk", strings.Repeat(big, 1500) + "END\n", 2, big + "END\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "f")
			os.WriteFile(p, []byte(tc.in), 0o644)
			f, _ := os.Open(p)
			defer f.Close()
			got, err := tailLines(f, tc.n)
			if err != nil || string(got) != tc.want {
				t.Errorf("got %q err %v", got, err)
			}
		})
	}
}

func TestImageTag(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM x\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte("p"), 0o644)
	a, err := imageTag("worker", "dev", dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := imageTag("worker", "dev", dir); a != b {
		t.Error("not stable")
	}
	os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte("q"), 0o644)
	if b, _ := imageTag("worker", "dev", dir); a == b {
		t.Error("content change ignored")
	}
	b, _ := imageTag("worker", "dev", dir)
	os.Chmod(filepath.Join(dir, "prompt.txt"), 0o755)
	if c, _ := imageTag("worker", "dev", dir); b == c {
		t.Error("mode change ignored")
	}
}

func TestRunArgv(t *testing.T) {
	l, _ := New(Options{Root: "/root-secret"})
	h := executor.Handle{JobID: "J1", Issue: 4, Stage: "scanner", Role: "cve", Container: "ard-J1", Image: "img:1",
		Worktree: "/root-secret/work/o-r/J1", RoleDir: "/repo/.ard/scanners/cve",
		View: executor.Paths{Work: "/work", Role: "/role"}}
	cmd := executor.Command{Path: "/role/scan", Args: []string{"--x"}, Env: []string{"ANTHROPIC_API_KEY=sk-secret"}}
	tests := []struct {
		name  string
		mut   func(*executor.Handle)
		stdin bool
		has   []string
		not   []string
	}{
		{"base", func(*executor.Handle) {}, false,
			[]string{"--rm", "--name", "ard-J1", "--user", "ard.job=J1", "ard.issue=4", "ard.stage=scanner", "ard.role=cve",
				"/root-secret/work/o-r/J1:/work", "/repo/.ard/scanners/cve:/role:ro", "ANTHROPIC_API_KEY", "ARD_WORK=/work"},
			[]string{"-i", "/sidecar:ro", "/work:ro"}},
		{"sidecar", func(h *executor.Handle) { h.SidecarDir = "/sc"; h.View.Sidecar = "/sidecar" }, false,
			[]string{"/sc:/sidecar:ro", "ARD_SIDECAR=/sidecar"}, nil},
		{"readonly", func(h *executor.Handle) { h.ReadOnly = true }, false,
			[]string{"/root-secret/work/o-r/J1:/work:ro"}, nil},
		{"stdin", func(*executor.Handle) {}, true, []string{"-i"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hh := h
			tc.mut(&hh)
			argv := l.runArgv(hh, cmd, tc.stdin)
			joined := " " + strings.Join(argv, " ") + " "
			for _, w := range tc.has {
				if !strings.Contains(joined, w) {
					t.Errorf("missing %q in %s", w, joined)
				}
			}
			for _, w := range tc.not {
				if strings.Contains(joined, " "+w+" ") || (w == "/work:ro" && strings.Contains(joined, ":/work:ro")) {
					t.Errorf("unexpected %q in %s", w, joined)
				}
			}
			for _, bad := range []string{"sk-secret", "docker.sock", ".ssh", "/.config"} {
				if strings.Contains(joined, bad) {
					t.Errorf("forbidden %q in argv", bad)
				}
			}
			for i, a := range argv {
				if a == "-v" && strings.HasPrefix(argv[i+1], "/root-secret:") {
					t.Error("Root mounted")
				}
			}
			if argv[len(argv)-2] != "--x" && argv[len(argv)-1] != "--x" {
				t.Error("command missing")
			}
		})
	}
}
