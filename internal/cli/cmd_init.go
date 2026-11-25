package cli

import (
	"context"

	"github.com/spf13/cobra"
)

func newInit(app *App) *cobra.Command {
	var pr bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write the starter .ard/ directory (--pr opens a PR instead)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.run(cmd, func(ctx context.Context, e *Env) error {
				if app.Handlers.Init == nil {
					return notImpl(cmd.CommandPath(), "#27")
				}
				return app.Handlers.Init(ctx, e, InitOpts{Version: app.Version, PR: pr})
			})
		},
	}
	cmd.Flags().BoolVar(&pr, "pr", false, "open a PR from branch ard-init instead of writing the local tree")
	return cmd
}
