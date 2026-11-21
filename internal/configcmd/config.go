// Package configcmd implements `ard config check`: load the resolved
// configuration, print it as YAML to stdout, print every problem to
// stderr, and exit non-zero when there is any problem.
package configcmd

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/config"
)

// Check is the handler behind `ard config check`. It prints the resolved
// configuration to stdout even when validation fails; the load error (all
// collected errors, one per line) is returned so the CLI layer prints it
// to stderr and exits non-zero.
func Check(_ context.Context, e *cli.Env) error {
	root, err := repoRoot(e.ConfigPath)
	if err != nil {
		return err
	}
	res, loadErr := config.Load(root)
	if res != nil {
		if yamlErr := res.WriteYAML(e.Stdout); yamlErr != nil {
			fmt.Fprintf(e.Stderr, "ard config check: rendering resolved config: %v\n", yamlErr)
		}
	}
	return loadErr
}

// repoRoot maps --config to the repository root Load expects: the default
// is the current directory; a path inside a .ard directory strips it, so
// --config repo/.ard/config.yaml checks repo.
func repoRoot(configPath string) (string, error) {
	if configPath == "" {
		return ".", nil
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return "", fmt.Errorf("ard config check: resolving --config: %w", err)
	}
	dir := filepath.Dir(abs)
	if filepath.Base(dir) == ".ard" {
		return filepath.Dir(dir), nil
	}
	return dir, nil
}
