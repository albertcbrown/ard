package jobpre

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/workspace"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/jobpod"
)

type fixture struct {
	base, secret string
	baseSHA      string
	roleSHA      string
	scSHA        string
	root         string
}

func commit(t *testing.T, dir, msg string) string {
	gittest.Run(t, dir, "add", "-A")
	gittest.Run(t, dir, "commit", "-m", msg)
	return strings.TrimSpace(gittest.Run(t, dir, "rev-parse", "HEAD"))
}

func write(t *testing.T, p, s string, mode os.FileMode) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
}

// newFixture builds file remotes o/r.git and o/side.git and redirects paths.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{root: root, base: filepath.Join(root, "remote"), secret: filepath.Join(root, "secret")}
	seed := filepath.Join(root, "seed")
	gittest.Run(t, root, "init", "--initial-branch=main", seed)
	write(t, filepath.Join(seed, "a.txt"), "a", 0o644)
	write(t, filepath.Join(seed, "run.sh"), "#!/bin/sh", 0o755)
	write(t, filepath.Join(seed, "scrub.txt"), "x", 0o644)
	write(t, filepath.Join(seed, "roles/eng/prompt.md"), "v1", 0o644)
	os.Symlink("a.txt", filepath.Join(seed, "link"))
	os.Symlink("a.txt", filepath.Join(seed, "in.txt"))
	f.baseSHA = commit(t, seed, "base")
	write(t, filepath.Join(seed, "roles/eng/prompt.md"), "v2", 0o644)
	f.roleSHA = commit(t, seed, "role")
	r := filepath.Join(f.base, "o", "r.git")
	os.MkdirAll(r, 0o755)
	gittest.Run(t, r, "init", "--bare", "--initial-branch=main", ".")
	gittest.Run(t, seed, "push", r, "main")
	gittest.Run(t, r, "config", "uploadpack.allowAnySHA1InWant", "true")

	sc := filepath.Join(root, "sc")
	gittest.Run(t, root, "init", "--initial-branch=main", sc)
	write(t, filepath.Join(sc, "doc.md"), "sidecar", 0o644)
	f.scSHA = commit(t, sc, "sc")
	sr := filepath.Join(f.base, "o", "side.git")
	os.MkdirAll(sr, 0o755)
	gittest.Run(t, sr, "init", "--bare", "--initial-branch=main", ".")
	gittest.Run(t, sc, "push", sr, "main")
	gittest.Run(t, sr, "config", "uploadpack.allowAnySHA1InWant", "true")

	paths = pathSet{
		secretDir: f.secret,
		ctl:       filepath.Join(root, "ctl"),
		work:      filepath.Join(root, "work"),
		job:       filepath.Join(root, "job"),
		bin:       filepath.Join(root, "bin"),
		agentBin:  filepath.Join(root, "bin", "ard"),
		termLog:   filepath.Join(root, "term"),
		role:      filepath.Join(root, "job", "role"),
		sidecar:   filepath.Join(root, "job", "sidecar"),
		history:   filepath.Join(root, "job", "history"),
	}
	for _, d := range []string{paths.ctl, paths.job, paths.bin, f.secret} {
		os.MkdirAll(d, 0o755)
	}
	t.Cleanup(func() { paths = pathSet{}; workspace.ChmodWritable(root) })
	t.Setenv("HOME", root)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "")
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })
	return f
}

func (f *fixture) manifest(t *testing.T, mod func(*jobpod.Manifest, *fixture)) {
	t.Helper()
	m := jobpod.Manifest{
		V: 1, Repo: "o/r", BaseSHA: f.baseSHA,
		Role: jobpod.Source{Repo: "o/r", SHA: f.roleSHA, Path: "roles/eng"},
	}
	if mod != nil {
		mod(&m, f)
	}
	b, err := jobpod.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.secret, jobpod.ItemManifest), string(b), 0o440)
	write(t, filepath.Join(f.secret, jobpod.ItemGitAccess), "tok-secret\n", 0o440)
}

func (f *fixture) run(base string) error {
	return Run(context.Background(), &cli.Env{}, cli.JobPreOpts{Deadline: time.Now().Add(time.Minute), GitBase: base})
}

func exitCode(err error) int {
	var ee *cli.ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return 0
}

func TestPre(t *testing.T) {
	tests := []struct {
		name     string
		mod      func(*jobpod.Manifest, *fixture)
		base     string // "" = file remote
		wantExit int
		check    func(t *testing.T, f *fixture)
	}{
		{"full layout", func(m *jobpod.Manifest, f *fixture) {
			m.Sidecar = &jobpod.Source{Repo: "o/side", SHA: f.scSHA}
			m.Scrub = []string{"scrub.txt"}
			m.Inputs = []executor.File{{Path: "sub/input.txt", Data: []byte("in")}}
		}, "", 0, func(t *testing.T, f *fixture) {
			if st, err := os.Stat(filepath.Join(paths.work, "run.sh")); err != nil || st.Mode().Perm()&0o100 == 0 {
				t.Errorf("exec bit: %v %v", st, err)
			}
			if d, err := os.Readlink(filepath.Join(paths.work, "link")); err != nil || d != "a.txt" {
				t.Errorf("symlink %q %v", d, err)
			}
			if _, err := os.Lstat(filepath.Join(paths.work, ".git")); err == nil {
				t.Error(".git in tree")
			}
			if _, err := os.Stat(filepath.Join(paths.work, "scrub.txt")); err == nil {
				t.Error("scrubbed file present")
			}
			if b, _ := os.ReadFile(filepath.Join(paths.work, "sub/input.txt")); string(b) != "in" {
				t.Errorf("input = %q", b)
			}
			if b, _ := os.ReadFile(filepath.Join(paths.work, "roles/eng/prompt.md")); string(b) != "v1" {
				t.Errorf("tree not at BaseSHA: %q", b)
			}
			if b, _ := os.ReadFile(filepath.Join(paths.role, "prompt.md")); string(b) != "v2" {
				t.Errorf("role not from Role.SHA: %q", b)
			}
			if b, _ := os.ReadFile(filepath.Join(paths.sidecar, "doc.md")); string(b) != "sidecar" {
				t.Errorf("sidecar = %q", b)
			}
			if st, _ := os.Stat(paths.work); st.Mode().Perm()&0o070 != 0o070 || st.Mode()&os.ModeSetgid == 0 {
				t.Errorf("work dir mode %v", st.Mode())
			}
			if st, _ := os.Stat(filepath.Join(paths.work, "a.txt")); st.Mode().Perm()&0o020 == 0 {
				t.Errorf("file not group-writable: %v", st.Mode())
			}
			if st, _ := os.Stat(filepath.Join(paths.role, "prompt.md")); st.Mode().Perm()&0o222 != 0 {
				t.Errorf("role writable: %v", st.Mode())
			}
			if _, err := os.Stat(filepath.Join(paths.job, "home")); err == nil {
				t.Error("pre made a home")
			}
			if st, err := os.Stat(paths.agentBin); err != nil || st.Mode().Perm() != 0o755 {
				t.Errorf("binary: %v %v", st, err)
			}
		}},
		{"read only tree", func(m *jobpod.Manifest, f *fixture) { m.ReadOnly = true }, "", 0, func(t *testing.T, f *fixture) {
			if st, _ := os.Stat(filepath.Join(paths.work, "a.txt")); st.Mode().Perm()&0o222 != 0 {
				t.Errorf("tree writable: %v", st.Mode())
			}
		}},
		{"history", func(m *jobpod.Manifest, f *fixture) { m.History = true }, "", 0, func(t *testing.T, f *fixture) {
			if _, err := os.Stat(filepath.Join(paths.history, "HEAD")); err != nil {
				t.Error(err)
			}
		}},
		{"missing base sha", func(m *jobpod.Manifest, f *fixture) { m.BaseSHA = strings.Repeat("1", 40) }, "", 3, func(t *testing.T, f *fixture) {
			if b, _ := os.ReadFile(paths.termLog); len(b) == 0 {
				t.Error("no termination message")
			}
		}},
		{"input over symlink", func(m *jobpod.Manifest, f *fixture) {
			m.Inputs = []executor.File{{Path: "in.txt", Data: []byte("x")}}
		}, "", 3, nil},
		{"role repo mismatch", func(m *jobpod.Manifest, f *fixture) { m.Role.Repo = "o/side" }, "", 3, nil},
		{"transient fetch failure", nil, "file:///nonexistent-dir", 1, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.manifest(t, tt.mod)
			base := tt.base
			if base == "" {
				base = "file://" + f.base
			}
			err := f.run(base)
			if got := exitCode(err); got != tt.wantExit {
				t.Fatalf("exit = %d (%v), want %d", got, err, tt.wantExit)
			}
			if err != nil && strings.Contains(err.Error(), "tok-secret") {
				t.Error("error leaks the token")
			}
			if tt.check != nil {
				tt.check(t, f)
			}
		})
	}
}

func TestPreBadSecrets(t *testing.T) {
	f := newFixture(t)
	f.manifest(t, nil)
	os.Remove(filepath.Join(f.secret, jobpod.ItemManifest))
	write(t, filepath.Join(f.secret, jobpod.ItemManifest), "{", 0o440)
	if got := exitCode(f.run("file://" + f.base)); got != 3 {
		t.Errorf("bad manifest exit = %d", got)
	}
	os.Remove(filepath.Join(f.secret, jobpod.ItemManifest))
	if got := exitCode(f.run("file://" + f.base)); got != 3 {
		t.Errorf("unreadable manifest exit = %d", got)
	}
	big := make([]byte, jobpod.MaxManifestBytes+1)
	write(t, filepath.Join(f.secret, jobpod.ItemManifest), string(big), 0o440)
	if got := exitCode(f.run("file://" + f.base)); got != 3 {
		t.Errorf("oversize manifest exit = %d", got)
	}
}

// TestPreTokenNotInArgv puts a fake git first on PATH. It records argv and
// env separately, then runs the real git.
func TestPreTokenNotInArgv(t *testing.T) {
	f := newFixture(t)
	f.manifest(t, nil)
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip(err)
	}
	bin := t.TempDir()
	argvLog, envLog := filepath.Join(bin, "argv"), filepath.Join(bin, "env")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + argvLog + "\nenv >> " + envLog + "\nexec " + real + " \"$@\"\n"
	write(t, filepath.Join(bin, "git"), script, 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if err := f.run("file://" + f.base); err != nil {
		t.Fatal(err)
	}
	argv, _ := os.ReadFile(argvLog)
	env, _ := os.ReadFile(envLog)
	cred := base64.StdEncoding.EncodeToString([]byte("x-access-token:tok-secret"))
	if len(argv) == 0 {
		t.Fatal("fake git was not used")
	}
	for _, form := range []string{"tok-secret", cred} {
		if strings.Contains(string(argv), form) {
			t.Errorf("token form %q in argv", form)
		}
	}
	if !strings.Contains(string(env), cred) {
		t.Error("token not passed through the environment")
	}
}
