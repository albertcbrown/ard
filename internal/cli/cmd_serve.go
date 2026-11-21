package cli

import (
	"context"

	"github.com/spf13/cobra"
)

func newServe(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the ard controller loops",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.run(cmd, func(ctx context.Context, e *Env) error {
				if app.Handlers.Serve == nil {
					return notImpl(cmd.CommandPath(), "#15")
				}
				return app.Handlers.Serve(ctx, e)
			})
		},
	}
}
