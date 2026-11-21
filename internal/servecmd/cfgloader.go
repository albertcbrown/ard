package servecmd

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/configsrc"
	"github.com/albertcbrown/ard/internal/github"
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
	src   *configsrc.Source
	fx    commenter
	log   *slog.Logger
	issue func() int // the status issue number; <= 0 when unknown or synthetic

	mu   sync.Mutex
	last string // the last error text posted
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
	return res, nil
}

func (c *cfgLoader) report(ctx context.Context, err error) {
	msg := err.Error()
	c.mu.Lock()
	defer c.mu.Unlock()
	if msg == c.last {
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
	c.last = err.Error()
}
