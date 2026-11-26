// Package configcmd implements `ard config check`: load the resolved
// configuration, print it as YAML to stdout, print every problem to
// stderr, and exit non-zero when there is any problem.
package configcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/local"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/labels"
)

// gitBin is the git binary the version check runs; empty means "git". Tests override it.
var gitBin = ""

// Check is the handler behind `ard config check`. It prints the resolved
// configuration to stdout even when validation fails; the load error (all
// collected errors, one per line) is returned so the CLI layer prints it
// to stderr and exits non-zero. With o.Ref set it validates that commit
// instead of the working tree; it does not fetch, so authoring stays offline.
func Check(ctx context.Context, e *cli.Env, o cli.ConfigCheckOpts) error {
	if err := gitx.New(gitBin).CheckVersion(ctx); err != nil {
		return fmt.Errorf("ard config check: %w", err)
	}
	root, err := config.RepoRoot(e.ConfigPath)
	if err != nil {
		return fmt.Errorf("ard config check: %w", err)
	}
	var res *config.Resolved
	var loadErr error
	if o.Ref == "" {
		res, loadErr = config.Load(root)
	} else if res, loadErr, err = loadRef(ctx, root, o.Ref); err != nil {
		return err
	}
	fmt.Fprintln(e.Stderr, "ard config check: github auth: "+authSummary())
	agentImage := os.Getenv("ARD_AGENT_IMAGE")
	if agentImage != "" {
		if ie := executor.CheckImageRef(agentImage); ie != nil {
			loadErr = errors.Join(loadErr, fmt.Errorf("ARD_AGENT_IMAGE: %w", ie))
		}
	}
	if res != nil {
		if ie := intakeErr(res); ie != nil {
			loadErr = errors.Join(loadErr, ie)
		}
		for _, r := range res.HostUntrustedWith(agentImage != "") {
			local.WarnHostRole(e.Log, string(r.Kind), r.Name)
			// Loud: also straight to stderr, whatever --log-level says.
			fmt.Fprint(e.Stderr, local.HostRoleText(string(r.Kind), r.Name))
		}
		for _, r := range res.HostUntrustedWith(agentImage != "") {
			if r.Network {
				fmt.Fprint(e.Stderr, local.HostNetworkText(string(r.Kind), r.Name))
			}
		}
		if res.Executor == "k8s" {
			fmt.Fprintln(e.Stderr, "ard config check: note: executor k8s: cluster settings come from `ard serve` flags")
		}
		if allReviewersGated(res) {
			fmt.Fprintln(e.Stderr, "ard config check: warning: every reviewer has a when: rule, so a PR that matches none of them gets no review and stays a draft")
		}
		if yamlErr := res.WriteYAML(e.Stdout); yamlErr != nil {
			fmt.Fprintf(e.Stderr, "ard config check: rendering resolved config: %v\n", yamlErr)
		}
	}
	return loadErr
}

// allReviewersGated reports whether reviewer roles exist and each has a when:
// rule.
func allReviewersGated(res *config.Resolved) bool {
	rs := res.RolesOf(config.KindReviewer)
	for _, r := range rs {
		if r.When == nil {
			return false
		}
	}
	return len(rs) > 0
}

// intakeErr rejects an intake label that is one of ard's own label names.
func intakeErr(res *config.Resolved) error {
	if res.Intake.Mode != config.IntakeLabel {
		return nil
	}
	if err := labels.CheckIntake(res.Intake.Label); err != nil {
		return &config.Error{File: ".ard/config.yaml", Key: "intake.label", Msg: err.Error()}
	}
	return nil
}

// loadRef loads the config at ref in root. err is a failure to resolve or
// export the ref; loadErr is the validation result.
func loadRef(ctx context.Context, root, ref string) (res *config.Resolved, loadErr, err error) {
	g := gitx.New("")
	sha, err := g.RevParse(ctx, root, ref)
	if err != nil {
		return nil, nil, fmt.Errorf("ard config check: resolve --ref %s: %w", ref, err)
	}
	tmp, err := os.MkdirTemp("", "ard-config-check-")
	if err != nil {
		return nil, nil, fmt.Errorf("ard config check: %w", err)
	}
	defer os.RemoveAll(tmp)
	snap := filepath.Join(tmp, "x")
	if err := g.ExportPaths(ctx, root, sha, snap, config.SourcePaths()...); err != nil {
		return nil, nil, fmt.Errorf("ard config check: --ref %s: %w", ref, err)
	}
	// Name the repo, not the temp directory, in the output.
	res, loadErr = config.LoadFS(os.DirFS(snap), root)
	res.SHA = sha
	return res, loadErr, nil
}

// authSummary says which identity ard serve would use, from the presence of
// environment variables only. It never runs gh or the network and prints no
// value.
func authSummary() string {
	auth, err := github.AuthFromEnv(os.Getenv, os.ReadFile)
	switch {
	case errors.Is(err, github.ErrNoCredentials):
		return "none (ard serve will try gh)"
	case err != nil:
		return "invalid (" + err.Error() + ")"
	case auth.Kind() == github.AuthApp:
		return "app (acts as the App's bot)"
	}
	return "token (ARD_GITHUB_TOKEN; acts as the token's user)"
}
