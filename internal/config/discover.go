package config

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// discover walks the role directories under .ard/{scanners,triage,workers,
// reviewers} and returns one Role per valid directory, plus an error per
// invalid one. A missing type directory is fine; so is a missing .ard.
//
// Conventions per directory:
//   - scanners need exactly one of an executable "scan" (deterministic) or
//     a non-empty "prompt.txt" (LLM);
//   - every other type needs a non-empty "prompt.txt" and must not have
//     "scan";
//   - an optional "Dockerfile" names the role image;
//   - every other file is an extra, mounted read-only beside the prompt.
//
// Numeric names (issue directories) and dot-names are skipped.
func discover(fsys fs.FS) ([]Role, Errors) {
	var roles []Role
	var errs Errors
	for _, kind := range kindOrder {
		dir := ".ard/" + kindDir[kind]
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			continue // type directory missing
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if isAllDigits(name) || strings.HasPrefix(name, ".") {
				continue
			}
			roleDir := dir + "/" + name
			if !validRoleName(name) {
				errs = append(errs, &Error{File: roleDir, Msg: fmt.Sprintf("invalid role name %q (must match %s)", name, roleNamePattern)})
				continue
			}
			role, ok := inspectRole(fsys, kind, name, roleDir, &errs)
			if ok {
				roles = append(roles, role)
			}
		}
	}
	return roles, errs
}

// inspectRole classifies one role directory. It returns ok=false when the
// directory is invalid, having recorded one or more errors.
func inspectRole(fsys fs.FS, kind Kind, name, dir string, errs *Errors) (Role, bool) {
	role := Role{Kind: kind, Name: name, Dir: dir}

	files := map[string]fs.FileInfo{}
	extras := map[string]bool{}
	var walk func(rel string) bool
	walk = func(rel string) bool {
		entries, err := fs.ReadDir(fsys, path.Join(dir, rel))
		if err != nil {
			*errs = append(*errs, &Error{File: dir, Key: rel, Msg: fmt.Sprintf("cannot read directory: %v", err)})
			return false
		}
		for _, entry := range entries {
			relName := path.Join(rel, entry.Name())
			if entry.IsDir() {
				if !walk(relName) {
					return false
				}
				continue
			}
			info, err := entry.Info()
			if err != nil {
				*errs = append(*errs, &Error{File: dir, Key: relName, Msg: fmt.Sprintf("cannot stat: %v", err)})
				return false
			}
			files[relName] = info
			extras[relName] = true
		}
		return true
	}
	if !walk(".") {
		return Role{}, false
	}

	promptInfo, hasPrompt := files["prompt.txt"]
	scanInfo, hasScan := files["scan"]
	if _, hasDockerfile := files["Dockerfile"]; hasDockerfile {
		role.Dockerfile = dir + "/Dockerfile"
		delete(extras, "Dockerfile")
	}

	switch kind {
	case KindScanner:
		switch {
		case hasPrompt && hasScan:
			*errs = append(*errs, &Error{File: dir, Msg: "has both scan and prompt.txt; a scanner is exactly one of them"})
			return Role{}, false
		case hasScan:
			if !isExecutable(scanInfo) {
				*errs = append(*errs, &Error{File: dir, Key: "scan", Msg: "must be executable (chmod +x) for a deterministic scanner"})
				return Role{}, false
			}
			role.ScanPath = dir + "/scan"
			delete(extras, "scan")
		case hasPrompt:
			if !nonEmpty(promptInfo) {
				*errs = append(*errs, &Error{File: dir, Key: "prompt.txt", Msg: "must not be empty"})
				return Role{}, false
			}
			role.PromptPath = dir + "/prompt.txt"
			delete(extras, "prompt.txt")
		default:
			*errs = append(*errs, &Error{File: dir, Msg: "needs an executable scan or a prompt.txt"})
			return Role{}, false
		}
	default:
		if hasScan {
			*errs = append(*errs, &Error{File: dir, Key: "scan", Msg: "only scanners may have a scan script"})
			return Role{}, false
		}
		if !hasPrompt {
			*errs = append(*errs, &Error{File: dir, Msg: "needs a prompt.txt"})
			return Role{}, false
		}
		if !nonEmpty(promptInfo) {
			*errs = append(*errs, &Error{File: dir, Key: "prompt.txt", Msg: "must not be empty"})
			return Role{}, false
		}
		role.PromptPath = dir + "/prompt.txt"
		delete(extras, "prompt.txt")
	}

	for rel := range extras {
		role.Extra = append(role.Extra, rel)
	}
	sort.Strings(role.Extra)
	return role, true
}

func isExecutable(info fs.FileInfo) bool { return info.Mode().Perm()&0o111 != 0 }

func nonEmpty(info fs.FileInfo) bool { return info.Size() > 0 }
