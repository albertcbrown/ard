package local

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/albertcbrown/ard/internal/executor"
)

var baseNames = map[string]bool{
	"PATH": true, "HOME": true, "LANG": true, "TMPDIR": true,
	"GIT_TERMINAL_PROMPT": true, "GIT_CONFIG_NOSYSTEM": true, "GIT_CONFIG_GLOBAL": true,
}

var ghRE = regexp.MustCompile(`(?i)^(GH|GITHUB)_|GITHUB`)

// ardEnv returns the ARD_* entries with the paths as the process sees them.
func ardEnv(h executor.Handle) []string {
	e := []string{
		"ARD_JOB=" + h.JobID,
		"ARD_ISSUE=" + strconv.Itoa(h.Issue),
		"ARD_STAGE=" + h.Stage,
		"ARD_ROLE=" + h.Role,
		"ARD_WORK=" + h.View.Work,
		"ARD_ROLE_DIR=" + h.View.Role,
	}
	if h.View.Sidecar != "" {
		e = append(e, "ARD_SIDECAR="+h.View.Sidecar)
	}
	return e
}

// hostEnv builds the environment of a host job from scratch. Nothing is
// inherited from the controller.
func (l *Local) hostEnv(h executor.Handle) []string {
	return append([]string{
		"PATH=" + l.pathEnv,
		"HOME=" + h.Home,
		"LANG=C.UTF-8",
		"TMPDIR=" + h.Home + "/tmp",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
	}, ardEnv(h)...)
}

// checkEnv validates Command.Env: only AllowEnv names may be added.
func (l *Local) checkEnv(env []string) error {
	// One credential per job: the CLI prefers the API key, so a second
	// entry would silently win or be ignored.
	if len(env) > 1 {
		return fmt.Errorf("%w: at most one credential per job", executor.ErrForbiddenEnv)
	}
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		switch {
		case !ok || name == "":
			return fmt.Errorf("%w: entry without name=value", executor.ErrForbiddenEnv)
		case ghRE.MatchString(name), baseNames[name], strings.HasPrefix(name, "ARD_"), name == "SSH_AUTH_SOCK":
			return fmt.Errorf("%w: %s", executor.ErrForbiddenEnv, name)
		case !l.allowEnv[name]:
			return fmt.Errorf("%w: %s is not in the allow list", executor.ErrForbiddenEnv, name)
		}
	}
	return nil
}

var secretRE = regexp.MustCompile(`(?i)TOKEN|SECRET|PASSWORD|KEY|CREDENTIAL`)

// checkFixed validates Command.Fixed: only AllowFixed names, and never the
// executor's own, GitHub or secret-shaped names.
func (l *Local) checkFixed(env []string) error {
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		switch {
		case !ok || name == "":
			return fmt.Errorf("%w: entry without name=value", executor.ErrForbiddenEnv)
		case ghRE.MatchString(name), baseNames[name], strings.HasPrefix(name, "ARD_"), name == "SSH_AUTH_SOCK", secretRE.MatchString(name):
			return fmt.Errorf("%w: %s", executor.ErrForbiddenEnv, name)
		case !l.allowFixed[name]:
			return fmt.Errorf("%w: %s is not in the fixed allow list", executor.ErrForbiddenEnv, name)
		}
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
	p := filepath.Join(h.Home, configDirName)
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
