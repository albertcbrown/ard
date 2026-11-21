package cli

import (
	"context"

	"github.com/spf13/cobra"
)

func newRebuildState(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild-state <scanner>",
		Short: "Rebuild a scanner's state database from GitHub",
		Args:  cobra.ExactArgs(1),
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
