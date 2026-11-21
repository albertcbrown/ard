package local

import (
	"fmt"
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
