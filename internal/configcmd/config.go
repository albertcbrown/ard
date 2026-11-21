// Package configcmd implements `ard config check`: load the resolved
// configuration, print it as YAML to stdout, print every problem to
// stderr, and exit non-zero when there is any problem.
package configcmd

import (
	"context"
	"fmt"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/config"
)

// Check is the handler behind `ard config check`. It prints the resolved
// configuration to stdout even when validation fails; the load error (all
// collected errors, one per line) is returned so the CLI layer prints it
// to stderr and exits non-zero.
func Check(_ context.Context, e *cli.Env) error {
	root, err := config.RepoRoot(e.ConfigPath)
	if err != nil {
		return fmt.Errorf("ard config check: %w", err)
	}
	res, loadErr := config.Load(root)
	if res != nil {
		if yamlErr := res.WriteYAML(e.Stdout); yamlErr != nil {
			fmt.Fprintf(e.Stderr, "ard config check: rendering resolved config: %v\n", yamlErr)
		}
	}
	return loadErr
}
