// Package configcmd implements `ard config check`: load the resolved
// configuration, print it as YAML to stdout, print every problem to
// stderr, and exit non-zero when there is any problem.
package configcmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/executor/local"
	"github.com/albertcbrown/ard/internal/gitx"
)

// Check is the handler behind `ard config check`. It prints the resolved
// configuration to stdout even when validation fails; the load error (all
// collected errors, one per line) is returned so the CLI layer prints it
// to stderr and exits non-zero. With o.Ref set it validates that commit
// instead of the working tree; it does not fetch, so authoring stays offline.
func Check(ctx context.Context, e *cli.Env, o cli.ConfigCheckOpts) error {
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
	if res != nil {
		for _, r := range res.HostUntrusted() {
			local.WarnHostRole(e.Log, string(r.Kind), r.Name)
			// Loud: also straight to stderr, whatever --log-level says.
			fmt.Fprint(e.Stderr, local.HostRoleText(string(r.Kind), r.Name))
		}
		if yamlErr := res.WriteYAML(e.Stdout); yamlErr != nil {
			fmt.Fprintf(e.Stderr, "ard config check: rendering resolved config: %v\n", yamlErr)
		}
	}
	return loadErr
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
