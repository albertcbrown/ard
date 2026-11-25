package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newServe(app *App) *cobra.Command {
	var opts ServeOpts
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the ard controller loops",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := opts.K8s.check(); err != nil {
				return &ExitError{Code: 2, Err: err}
			}
			return app.run(cmd, func(ctx context.Context, e *Env) error {
				if app.Handlers.Serve == nil {
					return notImpl(cmd.CommandPath(), "#15")
				}
				return app.Handlers.Serve(ctx, e, opts)
			})
		},
	}
	k := &opts.K8s
	envStr := func(name, def string) string {
		if v := os.Getenv(name); v != "" {
			return v
		}
		return def
	}
	envDur := func(name string, def time.Duration) time.Duration {
		if d, err := time.ParseDuration(os.Getenv(name)); err == nil && d > 0 {
			return d
		}
		return def
	}
	f := cmd.Flags()
	f.StringVar(&k.Namespace, "k8s-namespace", envStr("ARD_K8S_NAMESPACE", ""), "executor k8s: namespace job pods run in (env ARD_K8S_NAMESPACE)")
	f.StringVar(&k.Instance, "k8s-instance", envStr("ARD_K8S_INSTANCE", ""), "executor k8s: app.kubernetes.io/instance label of this install (env ARD_K8S_INSTANCE)")
	f.StringVar(&k.AgentImage, "k8s-agent-image", envStr("ARD_K8S_AGENT_IMAGE", ""), "executor k8s: agent image, pinned by @sha256: digest (env ARD_K8S_AGENT_IMAGE)")
	f.StringVar(&k.PodImage, "k8s-pod-image", envStr("ARD_K8S_POD_IMAGE", ""), "executor k8s: image of the pre and post containers, pinned by @sha256: digest (env ARD_K8S_POD_IMAGE)")
	f.StringVar(&k.GitBase, "k8s-git-base", envStr("ARD_K8S_GIT_BASE", "https://github.com"), "executor k8s: git host base URL (env ARD_K8S_GIT_BASE)")
	f.StringVar(&k.Resources, "k8s-resources", envStr("ARD_K8S_RESOURCES", ""), "executor k8s: resource overrides, e.g. cpu=1,memory=2Gi (env ARD_K8S_RESOURCES)")
	f.DurationVar(&k.PreBudget, "k8s-pre-budget", envDur("ARD_K8S_PRE_BUDGET", 5*time.Minute), "executor k8s: time budget of the pre (clone) step (env ARD_K8S_PRE_BUDGET)")
	f.DurationVar(&k.ResultGrace, "k8s-result-grace", envDur("ARD_K8S_RESULT_GRACE", 2*time.Minute), "executor k8s: grace for the result to arrive after the agent ends (env ARD_K8S_RESULT_GRACE)")
	cmd.Flags().StringSliceVar(&opts.Loops, "loops", nil,
		"run only these controller loops (comma-separated: scanners, stale, triage, workers, reviewers, reviewfix, merger, janitor, reaper); default every loop")
	var ui, noUI bool
	f.BoolVar(&ui, "ui", false, "draw the read-only command center (default: only when stdin and stdout are a terminal)")
	f.BoolVar(&noUI, "no-ui", false, "never draw the command center; keep the plain log output")
	cmd.MarkFlagsMutuallyExclusive("ui", "no-ui")
	cmd.PreRunE = func(*cobra.Command, []string) error {
		switch {
		case ui:
			opts.UI = "on"
		case noUI:
			opts.UI = "off"
		}
		return nil
	}
	return cmd
}

// check refuses an image that is not pinned by digest. Empty images are fine
// here: they are required only when the config selects executor k8s.
func (k K8sOpts) check() error {
	for _, im := range []struct{ flag, v string }{{"--k8s-agent-image", k.AgentImage}, {"--k8s-pod-image", k.PodImage}} {
		if im.v != "" && !strings.Contains(im.v, "@sha256:") {
			return fmt.Errorf("ard serve: %s must be pinned by digest (name@sha256:...)", im.flag)
		}
	}
	return nil
}
