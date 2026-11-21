package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersion(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the ard version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "ard %s\n", app.Version)
			return nil
		},
	}
}
