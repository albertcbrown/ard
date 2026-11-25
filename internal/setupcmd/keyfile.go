package setupcmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultKeyPath is ~/.ard/keys/<slug>.pem.
func DefaultKeyPath(slug string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("setup: cannot resolve the home directory; pass --key-file")
	}
	return filepath.Join(home, ".ard", "keys", slug+".pem"), nil
}

// CheckKeyPath fails when path exists and force is not set.
func CheckKeyPath(path string, force bool) error {
	if _, err := os.Lstat(path); err == nil && !force {
		return fmt.Errorf("setup: %s already exists; use --force to overwrite", path)
	}
	return nil
}

// WriteKey writes the private key with mode 0600. The parent directory is
// created with 0700. An existing path is refused unless force is set.
func WriteKey(path string, pem []byte, force bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("setup: create key directory: %w", err)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("setup: %s already exists; use --force to overwrite", path)
		}
		return fmt.Errorf("setup: write key file: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("setup: chmod key file: %w", err)
	}
	if _, err := f.Write(pem); err != nil {
		f.Close()
		return fmt.Errorf("setup: write key file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("setup: write key file: %w", err)
	}
	return nil
}
