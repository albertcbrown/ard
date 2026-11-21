package cli

import (
	"context"

	"github.com/spf13/cobra"
)

func newInit(app *App) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a starting .ard/ directory into the repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.run(cmd, func(ctx context.Context, e *Env) error {
				if app.Handlers.Init == nil {
					return notImpl(cmd.CommandPath(), "#27")
				}
				return app.Handlers.Init(ctx, e, InitOpts{Force: force})
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing .ard/ directory")
	return cmd
}
