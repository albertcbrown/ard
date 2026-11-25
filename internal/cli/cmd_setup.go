package cli

import (
	"context"

	"github.com/spf13/cobra"
)

func newSetup(app *App) *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Check the repository setup, then apply the fixes (exit 2 on drift with --check)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.run(cmd, func(ctx context.Context, e *Env) error {
				if app.Handlers.Setup == nil {
					return notImpl(cmd.CommandPath(), "#346")
				}
				return app.Handlers.Setup(ctx, e, SetupOpts{Check: check})
			})
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "report only; exit 2 when anything has drifted")
	return cmd
}
