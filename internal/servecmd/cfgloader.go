package servecmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/configsrc"
	"github.com/albertcbrown/ard/internal/executor/local"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/redact"
)

// maxConfigErrorBytes caps the error text posted to the status issue.
const maxConfigErrorBytes = 4000

type commenter interface {
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
}

// cfgLoader loads config through a configsrc.Source and, when the load fails,
// tells the status issue once per distinct error. A successful load clears
// the memory, so the same failure after a recovery is reported again. It is
// safe for concurrent use: every loop calls it each tick.
type cfgLoader struct {
	src *configsrc.Source
	fx  commenter
	log *slog.Logger
	// stderr receives the host-mode warning in plain text, so it survives
	// --log-level. nil = log only.
	stderr io.Writer
	issue  func() int // the status issue number; <= 0 when unknown or synthetic
	// paths replaces the controller host's paths in the error text posted to
	// the status issue. nil = post a fixed message, never the error.
	paths *redact.Secrets

	mu   sync.Mutex
	last string // the last error text posted
	// warned holds the roles already warned about as host-mode, by kind/name.
	warned map[string]bool
	// badIntake holds the invalid intake labels already logged.
	badIntake map[string]bool
}

func (c *cfgLoader) Load(ctx context.Context) (*config.Resolved, error) {
	res, err := c.src.Load(ctx)
	if err != nil {
		c.report(ctx, err)
		return nil, err
	}
	c.mu.Lock()
	c.last = ""
	c.mu.Unlock()
	c.warnHost(res)
	c.warnIntake(res)
	return res, nil
}

// warnHost logs executor.host_untrusted once per role per process, and writes
// it to stderr: at the first successful load (startup, or the first tick
// after a startup that could not load) and for any role a later config adds.
func (c *cfgLoader) warnHost(res *config.Resolved) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range res.HostUntrusted() {
		key := string(r.Kind) + "/" + r.Name
		if c.warned[key] {
			continue
		}
		if c.warned == nil {
			c.warned = map[string]bool{}
		}
		c.warned[key] = true
		local.WarnHostRole(c.log, string(r.Kind), r.Name)
		if c.stderr != nil {
			fmt.Fprint(c.stderr, local.HostRoleText(string(r.Kind), r.Name))
		}
	}
}

// warnIntake logs config.intake_invalid once per distinct invalid intake
// label. The config still loads: intake.Admit skips every issue instead.
func (c *cfgLoader) warnIntake(res *config.Resolved) {
	if res.Intake.Mode != config.IntakeLabel {
		return
	}
	err := labels.CheckIntake(res.Intake.Label)
	if err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.badIntake[res.Intake.Label] {
		return
	}
	if c.badIntake == nil {
		c.badIntake = map[string]bool{}
	}
	c.badIntake[res.Intake.Label] = true
	c.log.Error("config.intake_invalid", "key", "intake.label", "label", res.Intake.Label, "error", err)
}

// newPathRedactor builds the host-path replacer for posted error text.
func newPathRedactor(host closeout.HostPaths) (*redact.Secrets, error) {
	return redact.NewSecrets().WithPaths(closeout.PathRules("", "", host)...)
}

func (c *cfgLoader) report(ctx context.Context, err error) {
	raw := err.Error()
	msg := "the error text is withheld because host paths could not be redacted; see the controller log"
	if c.paths != nil {
		msg = c.paths.String(raw)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if raw == c.last {
		return
	}
	n := c.issue()
	if n <= 0 {
		return // not remembered: report once the status issue exists
	}
	if len(msg) > maxConfigErrorBytes {
		msg = msg[:maxConfigErrorBytes] + "\n... (truncated)"
	}
	// The text can carry file and key names from the repo: keep it inside the
	// code fence.
	msg = strings.ReplaceAll(msg, "```", "'''")
	body := fmt.Sprintf("ard cannot load its configuration from `origin/main`, so it is paused. "+
		"It retries every tick and resumes once the config loads cleanly.\n\n```\n%s\n```\n", msg)
	if _, perr := c.fx.CreateComment(ctx, n, body); perr != nil {
		c.log.Warn("serve.config_error_not_posted", "issue", n, "error", perr)
		return
	}
	c.last = raw
}
