package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// NewRoot builds the ard command tree. Global flags live on the root as
// persistent flags; PersistentPreRunE validates them and builds the Env
// every handler receives. The default completion command is disabled so
// `ard help` lists exactly the ard commands.
func NewRoot(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:   "ard",
		Short: "ard - automated repository development",
		Long: `ard makes a GitHub repository maintain itself: scanners file
deduplicated issues, triage agents turn each issue into a plan, a worker
implements it on a branch, reviewer agents critique the PR, and the merger
lands it once every review passes.`,

		Version:           app.Version,
		SilenceErrors:     true,
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return app.setup(cmd)
		},
	}
	root.SetVersionTemplate("ard {{.Version}}\n")

	g := &app.Globals
	root.PersistentFlags().StringVar(&g.Repo, "repo", "",
		`target repository as "owner/repo" (default: resolved from config)`)
	root.PersistentFlags().StringVar(&g.ConfigPath, "config", "",
		"path to config.yaml (default: .ard/config.yaml in the target repo)")
	root.PersistentFlags().BoolVar(&g.DryRun, "dry-run", false,
		"log every side effect without performing it")
	root.PersistentFlags().StringVar(&g.LogLevel, "log-level", "info",
		"log level: debug, info, warn or error")

	root.AddCommand(
		newServe(app),
		newConfig(app),
		newLabels(app),
		newRebuildState(app),
		newInit(app),
		newVersion(app),
	)
	return root
}

// setup validates the global flags and builds the Env handlers receive.
func (a *App) setup(cmd *cobra.Command) error {
	if err := validateRepo(a.Globals.Repo); err != nil {
		return err
	}
	log, err := NewLogger(a.Globals.LogLevel, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	a.env = &Env{
		Globals: a.Globals,
		Log:     log,
		Stdout:  cmd.OutOrStdout(),
		Stderr:  cmd.ErrOrStderr(),
	}
	return nil
}

// validateRepo checks the --repo shape: exactly "owner/repo" when set.
func validateRepo(repo string) error {
	if repo == "" {
		return nil
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("--repo must be \"owner/repo\", got %q", repo)
	}
	return nil
}
