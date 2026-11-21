package config

import (
	"errors"
	"io/fs"

	"gopkg.in/yaml.v3"
)

// SourcePaths lists the repo-relative paths a config load reads: the config
// file and the four role-directory trees. It is what a snapshot of a commit
// must contain. The .ard/<issue#>/ directories, with their logs, are not in
// it. The list is built from kindDir and kindOrder so it cannot drift from
// discover.
func SourcePaths() []string {
	paths := []string{".ard/config.yaml"}
	for _, k := range kindOrder {
		paths = append(paths, ".ard/"+kindDir[k])
	}
	return paths
}

// LocalPaused reports whether the config file in fsys (a working tree) says
// `paused: true`. It reads only that key, and it never fails closed: a file
// that is missing, unreadable or not valid YAML reports false with the error
// (nil for a missing file), so the caller can log it. Every other setting
// comes from the commit the controller runs from.
func LocalPaused(fsys fs.FS) (bool, error) {
	data, err := fs.ReadFile(fsys, ".ard/config.yaml")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var v struct {
		Paused bool `yaml:"paused"`
	}
	if err := yaml.Unmarshal(data, &v); err != nil {
		return false, err
	}
	return v.Paused, nil
}
