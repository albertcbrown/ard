package executor

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var baseNames = map[string]bool{
	"PATH": true, "HOME": true, "LANG": true, "TMPDIR": true,
	"GIT_TERMINAL_PROMPT": true, "GIT_CONFIG_NOSYSTEM": true, "GIT_CONFIG_GLOBAL": true,
}

var ghRE = regexp.MustCompile(`(?i)^(GH|GITHUB)_|GITHUB`)

var secretRE = regexp.MustCompile(`(?i)TOKEN|SECRET|PASSWORD|KEY|CREDENTIAL`)

// ArdEnv returns the ARD_* entries with the paths as the process sees them.
func ArdEnv(h Handle) []string {
	e := []string{
		"ARD_JOB=" + h.JobID,
		"ARD_ISSUE=" + strconv.Itoa(h.Issue),
		"ARD_STAGE=" + h.Stage,
		"ARD_ROLE=" + h.Role,
		"ARD_WORK=" + h.View.Work,
		"ARD_ROLE_DIR=" + h.View.Role,
	}
	if h.Network != NetworkOn {
		e = append(e, "ARD_NETWORK=0")
	} else {
		e = append(e, "ARD_NETWORK=1")
	}
	if h.View.Sidecar != "" {
		e = append(e, "ARD_SIDECAR="+h.View.Sidecar)
	}
	if h.View.History != "" {
		e = append(e, "ARD_HISTORY="+h.View.History)
	}
	if h.View.Out != "" {
		e = append(e, "ARD_OUT="+h.View.Out)
	}
	return e
}

// EnvPolicy says which names a job's Command.Env and Command.Fixed may carry.
type EnvPolicy struct {
	AllowEnv, AllowFixed map[string]bool
}

// CheckEnv validates Command.Env: only AllowEnv names may be added.
func (p EnvPolicy) CheckEnv(env []string) error {
	// One credential per job: the CLI prefers the API key, so a second
	// entry would silently win or be ignored.
	if len(env) > 1 {
		return fmt.Errorf("%w: at most one credential per job", ErrForbiddenEnv)
	}
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		switch {
		case !ok || name == "":
			return fmt.Errorf("%w: entry without name=value", ErrForbiddenEnv)
		case ghRE.MatchString(name), baseNames[name], strings.HasPrefix(name, "ARD_"), name == "SSH_AUTH_SOCK":
			return fmt.Errorf("%w: %s", ErrForbiddenEnv, name)
		case !p.AllowEnv[name]:
			return fmt.Errorf("%w: %s is not in the allow list", ErrForbiddenEnv, name)
		}
	}
	return nil
}

// CheckFixed validates Command.Fixed: only AllowFixed names, and never the
// executor's own, GitHub or secret-shaped names.
func (p EnvPolicy) CheckFixed(env []string) error {
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		switch {
		case !ok || name == "":
			return fmt.Errorf("%w: entry without name=value", ErrForbiddenEnv)
		case ghRE.MatchString(name), baseNames[name], strings.HasPrefix(name, "ARD_"), name == "SSH_AUTH_SOCK", secretRE.MatchString(name):
			return fmt.Errorf("%w: %s", ErrForbiddenEnv, name)
		case !p.AllowFixed[name]:
			return fmt.Errorf("%w: %s is not in the fixed allow list", ErrForbiddenEnv, name)
		}
	}
	return nil
}

// IsGitHubEnvName reports whether name looks like a GitHub credential
// variable; executors drop these from what they pass on.
func IsGitHubEnvName(name string) bool { return ghRE.MatchString(name) }
