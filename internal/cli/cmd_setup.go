package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newSetup(app *App) *cobra.Command {
	var o SetupOpts
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Check the repository setup, then apply the fixes (exit 2 on drift with --check)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.run(cmd, func(ctx context.Context, e *Env) error {
				if o.PrintWorkflow && (o.Check || o.CreateGHApp || o.AppName != "" || o.KeyFile != "" || o.Force) {
					return fmt.Errorf("setup: --print-workflow cannot be combined with other flags")
				}
				if o.CreateGHApp && o.Check {
					return fmt.Errorf("setup: --create-gh-app cannot be combined with --check")
				}
				if !o.CreateGHApp && (o.AppName != "" || o.KeyFile != "" || o.Force) {
					return fmt.Errorf("setup: --app-name, --key-file and --force need --create-gh-app")
				}
				if app.Handlers.Setup == nil {
					return notImpl(cmd.CommandPath(), "#346")
				}
				return app.Handlers.Setup(ctx, e, o)
			})
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.Check, "check", false, "report only; exit 2 when anything has drifted")
	f.BoolVar(&o.CreateGHApp, "create-gh-app", false, "create ard's GitHub App with the manifest flow, then run the checks")
	f.StringVar(&o.AppName, "app-name", "", "App name for --create-gh-app (default ard-<owner>-<repo>)")
	f.StringVar(&o.KeyFile, "key-file", "", "private key path for --create-gh-app (default ~/.ard/keys/<slug>.pem)")
	f.BoolVar(&o.PrintWorkflow, "print-workflow", false, "print the bot-commits workflow embedded in ard and exit")
	f.BoolVar(&o.Force, "force", false, "overwrite an existing key file")
	return cmd
}
