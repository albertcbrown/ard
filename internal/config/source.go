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

// SharedSourcePaths lists the role-directory trees a shared roles repo
// contributes: SourcePaths without the config file.
func SharedSourcePaths() []string { return SourcePaths()[1:] }

// SharedRoles reads only the roles_repo key of the config file in fsys. A
// missing file or an unset key returns nil, nil. The repo and ref are not
// validated here; config.Load does that.
func SharedRoles(fsys fs.FS) (*Sidecar, error) {
	data, err := fs.ReadFile(fsys, ".ard/config.yaml")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var v struct {
		RolesRepo *struct {
			Repo string `yaml:"repo"`
			Ref  string `yaml:"ref"`
		} `yaml:"roles_repo"`
	}
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	if v.RolesRepo == nil {
		return nil, nil
	}
	sc := &Sidecar{Repo: v.RolesRepo.Repo, Ref: v.RolesRepo.Ref}
	if sc.Ref == "" {
		sc.Ref = defaultSidecarRef
	}
	return sc, nil
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
