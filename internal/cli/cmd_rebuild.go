package cli

import (
	"context"

	"github.com/spf13/cobra"
)

func newRebuildState(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild-state <scanner>",
		Short: "Rebuild a scanner's state database from GitHub",
		Long: `Rebuild a scanner's state database from the hidden markers on its GitHub
issues, open and closed, and replace the findings and suppressions in one
transaction. The run history is kept.

Stop ard serve first: a running controller holds the leader lock and the
command refuses to run. With --dry-run the report is printed and nothing is
written. The dry-run state database holds synthetic issues that are not on
GitHub, so it is never rebuilt. Only scanners with a scan executable can be
rebuilt.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.run(cmd, func(ctx context.Context, e *Env) error {
				if app.Handlers.RebuildState == nil {
					return notImpl(cmd.CommandPath(), "#23")
				}
				return app.Handlers.RebuildState(ctx, e, args[0])
			})
		},
	}
}
