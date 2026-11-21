package config

import (
	"fmt"
	"path/filepath"
)

// RepoRoot maps --config to the repository root Load expects: the default
// is the current directory; a path inside a .ard directory strips it, so
// --config repo/.ard/config.yaml names repo.
func RepoRoot(configPath string) (string, error) {
	if configPath == "" {
		return ".", nil
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return "", fmt.Errorf("resolving --config: %w", err)
	}
	dir := filepath.Dir(abs)
	if filepath.Base(dir) == ".ard" {
		return filepath.Dir(dir), nil
	}
	return dir, nil
}
