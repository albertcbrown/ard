package cli

import (
	"context"

	"github.com/spf13/cobra"
)

func newLabels(app *App) *cobra.Command {
	labels := &cobra.Command{
		Use:   "labels",
		Short: "Manage the ard: label set on the repository",
		// No RunE: bare `ard labels` prints the help listing sync.
	}
	labels.AddCommand(&cobra.Command{
		Use:   "sync",
		Short: "Create or update the ard: labels on the repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.run(cmd, func(ctx context.Context, e *Env) error {
				if app.Handlers.LabelsSync == nil {
					return notImpl(cmd.CommandPath(), "#22")
				}
				return app.Handlers.LabelsSync(ctx, e)
			})
		},
	})
	return labels
}
