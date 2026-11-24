package local

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/albertcbrown/ard/internal/executor"
)

// EventHostUntrusted is the slog message of every host-mode warning.
const EventHostUntrusted = "executor.host_untrusted"

// EventHostNetwork is the slog message for a host job with network: false.
const EventHostNetwork = "executor.host_network_not_enforced"

const hostHint = "add a Dockerfile to the role directory, or set ARD_AGENT_IMAGE, to run it under Docker"

// HostReach lists what a host-mode job can reach as the controller's uid.
// It returns a fresh slice.
func HostReach() []string {
	return []string{
		"the GitHub App private key file (ARD_GITHUB_PRIVATE_KEY_FILE) by absolute path",
		"~/.config/gh",
		"write access to the controller's clone (~/.ard/repos/<owner>/<repo>.git)",
		"~/.ard: controller state, config snapshots, the pause file and other jobs' worktrees and logs",
		"undoing chmod a-w on a read-only worktree",
	}
}

// WarnHostRole logs one WARN for a role that reads untrusted input and has
// no Dockerfile. kind and name come from config.Role; this package does not
// import config.
func WarnHostRole(log *slog.Logger, kind, name string) {
	log.Warn(EventHostUntrusted, "kind", kind, "role", name,
		"reaches", strings.Join(HostReach(), "; "), "hint", hostHint)
}

// HostRoleText is the plain-text form of the warning, for writing straight to
// stderr so it survives --log-level. It ends in a newline.
func HostRoleText(kind, name string) string {
	return fmt.Sprintf("WARNING: %s role %q reads untrusted input but has no Dockerfile and ARD_AGENT_IMAGE is not set, so its jobs run as a host subprocess "+
		"under the controller's uid and can reach: %s. Hint: %s.\n",
		kind, name, strings.Join(HostReach(), "; "), hostHint)
}

// hostUntrusted reports whether a started job should warn: host mode and
// not marked TrustedInput.
func hostUntrusted(h executor.Handle) bool { return h.Kind == "host" && !h.TrustedInput }

// warnHostJob logs the per-job WARN and calls OnHostUntrusted. A hook error
// is logged and does not fail Start: the job is already running.
func (l *Local) warnHostJob(ctx context.Context, h executor.Handle) {
	l.log.Warn(EventHostUntrusted, "job", h.JobID, "issue", h.Issue, "stage", h.Stage, "role", h.Role,
		"reaches", strings.Join(HostReach(), "; "), "hint", hostHint)
	if h.Network != executor.NetworkOn {
		l.warnHostNetwork(h)
	}
	if l.onHostUntrusted == nil {
		return
	}
	if err := l.onHostUntrusted(ctx, h); err != nil {
		l.log.Error("executor.host_untrusted_not_recorded", "job", h.JobID, "error", err)
	}
}

// warnHostNetwork logs that a host job's network: false is advice only.
func (l *Local) warnHostNetwork(h executor.Handle) {
	l.log.Warn(EventHostNetwork, "job", h.JobID, "issue", h.Issue, "stage", h.Stage, "role", h.Role,
		"msg", "network: false not enforced in host mode")
}

// HostNetworkText is the config check warning for a host-mode role that reads
// untrusted input with network on. It ends in a newline.
func HostNetworkText(kind, name string) string {
	return fmt.Sprintf("WARNING: %s role %q reads untrusted input in host mode with network: true, so a prompt-injected agent "+
		"can send out anything it reaches, including the GitHub App private key file (ARD_GITHUB_PRIVATE_KEY_FILE). Hint: %s.\n",
		kind, name, hostHint)
}
