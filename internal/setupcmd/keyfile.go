package setupcmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("setup: %s is a symlink; refusing to write the key through it", path)
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

// WriteKeyWithFallback writes the key to path. GitHub returns the private key
// only once, so when that write fails (for example the default file exists or
// the directory is unwritable) it falls back to a fresh <name>-<timestamp>.pem
// created with O_EXCL, first beside path, then under ~/.ard/keys, then in the
// temp directory. It returns the path actually used.
func WriteKeyWithFallback(path string, pem []byte, force bool) (string, error) {
	firstErr := WriteKey(path, pem, force)
	if firstErr == nil {
		return path, nil
	}
	base := strings.TrimSuffix(filepath.Base(path), ".pem") + "-" + time.Now().UTC().Format("20060102T150405.000000000") + ".pem"
	dirs := []string{filepath.Dir(path)}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".ard", "keys"))
	}
	dirs = append(dirs, os.TempDir())
	for _, d := range dirs {
		alt := filepath.Join(d, base)
		if WriteKey(alt, pem, false) == nil {
			return alt, nil
		}
	}
	return "", firstErr
}
