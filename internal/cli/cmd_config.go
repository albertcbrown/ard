package cli

import (
	"context"

	"github.com/spf13/cobra"
)

func newConfig(app *App) *cobra.Command {
	cfg := &cobra.Command{
		Use:   "config",
		Short: "Inspect and validate ard configuration",
		// No RunE: bare `ard config` prints the help listing check.
	}
	var opts ConfigCheckOpts
	check := &cobra.Command{
		Use:   "check",
		Short: "Validate .ard/config.yaml and the role directories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.run(cmd, func(ctx context.Context, e *Env) error {
				if app.Handlers.ConfigCheck == nil {
					return notImpl(cmd.CommandPath(), "#11")
				}
				return app.Handlers.ConfigCheck(ctx, e, opts)
			})
		},
	}
	check.Flags().StringVar(&opts.Ref, "ref", "",
		"validate the config at a commit instead of the working tree, e.g. --ref origin/main after `git fetch`; ard serve reads origin/main")
	cfg.AddCommand(check)
	return cfg
}
