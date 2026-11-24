package local

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/workspace"
)

// hostEnv builds the environment of a host job from scratch. Nothing is
// inherited from the controller.
func (l *Local) hostEnv(h executor.Handle) []string {
	return append([]string{
		"PATH=" + l.pathEnv,
		"HOME=" + h.Home,
		"LANG=C.UTF-8",
		"GOFLAGS=-modcacherw",
		"TMPDIR=" + h.Home + "/tmp",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
	}, executor.ArdEnv(h)...)
}

// checkNetwork rejects a job that would run with no network but needs the
// model API: a credential in Command.Env means an agent step.
func checkNetwork(h executor.Handle, c executor.Command) error {
	if h.Network == executor.NetworkNone && len(c.Env) > 0 {
		return fmt.Errorf("%w: network none with a credential in env", executor.ErrForbiddenEnv)
	}
	return nil
}

// checkConfigDir keeps a job on its own config dir. A Fixed entry for the
// config-dir variable must equal View.ConfigDir, and a prepared dir must
// still be the empty 0700 directory Prepare made.
func (l *Local) checkConfigDir(h executor.Handle, fixed []string) error {
	for _, kv := range fixed {
		name, val, _ := strings.Cut(kv, "=")
		if name == l.configDirEnv && (h.View.ConfigDir == "" || val != h.View.ConfigDir) {
			return fmt.Errorf("%w: %s must be the job config dir", executor.ErrForbiddenEnv, name)
		}
	}
	if h.View.ConfigDir == "" {
		return nil
	}
	p := filepath.Join(h.Home, workspace.ConfigDirName)
	want := p
	if h.Kind == "docker" {
		want = "/ard-config"
	}
	if h.View.ConfigDir != want {
		return fmt.Errorf("%w: config dir %q, want %q", executor.ErrNotPrepared, h.View.ConfigDir, want)
	}
	if rel, err := filepath.Rel(l.root, h.Home); err != nil || rel == ".." || strings.HasPrefix(rel, "../") || rel == "." {
		return fmt.Errorf("%w: job home %s is not under %s", executor.ErrNotPrepared, h.Home, l.root)
	}
	if hst, err := os.Lstat(h.Home); err != nil || !hst.IsDir() {
		return fmt.Errorf("%w: job home %s is not a directory", executor.ErrNotPrepared, h.Home)
	}
	fail := func(why string) error {
		return fmt.Errorf("%w: config dir %s: %s", executor.ErrNotPrepared, p, why)
	}
	st, err := os.Lstat(p)
	switch {
	case err != nil:
		return fail(err.Error())
	case !st.IsDir():
		return fail("not a directory")
	case st.Mode().Perm() != 0o700:
		return fail(fmt.Sprintf("mode %o, want 700", st.Mode().Perm()))
	}
	ents, err := os.ReadDir(p)
	if err != nil {
		return fail(err.Error())
	}
	if len(ents) != 0 {
		return fail("not empty")
	}
	return nil
}

// policy is the env allow-list policy of this executor.
func (l *Local) policy() executor.EnvPolicy {
	return executor.EnvPolicy{AllowEnv: l.allowEnv, AllowFixed: l.allowFixed}
}
