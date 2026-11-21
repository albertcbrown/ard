package effects

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
)

// Options configures Open.
type Options struct {
	DryRun     bool
	GitHub     github.Config // passed to github.New
	Client     github.Client // tests only: used instead of github.New when non-nil
	Git        *gitx.Git     // nil = gitx.New("")
	LedgerPath string        // dry-run only; "" = no ledger file
	Redact     func(string) string
	Log        *slog.Logger
}

// readOnly exposes only the Reader. Because the field is an interface of
// type github.Reader, a type assertion cannot recover the underlying Writer.
type readOnly struct{ github.Reader }

// Open is the production factory and the only place the GitHub client is
// built. Live mode returns Effects backed by the client's Writer. Dry-run
// builds the client read-only (github.Config.ReadOnly) and returns a DryRun
// that holds no writer. Either way the second result is a read-only view.
func Open(ctx context.Context, o Options) (Effects, github.Reader, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("effects: open: %w", err)
	}
	g := o.Git
	if g == nil {
		g = gitx.New("")
	}
	client := o.Client
	if client == nil {
		cfg := o.GitHub
		cfg.ReadOnly = o.DryRun
		c, err := github.New(cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("effects: open github client: %w", err)
		}
		client = c
	}
	if o.DryRun {
		d, err := NewDryRun(DryRunOptions{Log: o.Log, LedgerPath: o.LedgerPath, Redact: o.Redact, Staged: g.StagedPaths})
		if err != nil {
			return nil, nil, err
		}
		return d, readOnly{client}, nil
	}
	return NewLive(client, g, o.Log), readOnly{client}, nil
}
