// Package docscheck keeps the user docs under docs/site honest: every ard
// command and flag they name must exist in the CLI, and the configuration
// reference must list exactly the keys the config package knows.
package docscheck

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

// IgnoreMarker on the line before a command skips the check for that line.
const IgnoreMarker = "<!-- docscheck:ignore -->"

var (
	inlineCode = regexp.MustCompile("`([^`\n]+)`")
	wordRe     = regexp.MustCompile(`^[a-z][a-z-]*$`)
	keyCell    = regexp.MustCompile("^\\|\\s*`([^`]+)`")
)

// CheckCommands scans every .md file in fsys. Commands in fenced blocks
// (a line starting with "ard", optionally after "$ ") and in inline code
// spans starting with "ard " are checked against paths, a map of command
// path ("ard serve") to its accepted flags. It returns one problem per
// bad command.
func CheckCommands(fsys fs.FS, paths map[string][]string) ([]string, error) {
	var problems []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		fenced, prev := false, ""
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			ignore := strings.TrimSpace(prev) == IgnoreMarker
			prev = line
			if strings.HasPrefix(trimmed, "```") {
				fenced = !fenced
				continue
			}
			var cmds []string
			if fenced {
				c := strings.TrimSpace(strings.TrimPrefix(trimmed, "$ "))
				if c == "ard" || strings.HasPrefix(c, "ard ") {
					cmds = append(cmds, c)
				}
			} else {
				for _, m := range inlineCode.FindAllStringSubmatch(line, -1) {
					if strings.HasPrefix(m[1], "ard ") {
						cmds = append(cmds, m[1])
					}
				}
			}
			if ignore {
				continue
			}
			for _, c := range cmds {
				if msg := checkOne(c, paths); msg != "" {
					problems = append(problems, fmt.Sprintf("%s:%d: %s", p, i+1, msg))
				}
			}
		}
		return nil
	})
	return problems, err
}

func checkOne(cmd string, paths map[string][]string) string {
	var toks []string
	for _, t := range strings.Fields(cmd) {
		if t == "|" || t == "&&" || t == ";" || t == ">" || strings.HasPrefix(t, "#") {
			break
		}
		toks = append(toks, t)
	}
	path, i := "ard", 1
	var flagToks []string
	for ; i < len(toks); i++ {
		if next := path + " " + toks[i]; !strings.HasPrefix(toks[i], "-") {
			if _, ok := paths[next]; ok {
				path = next
				continue
			}
		}
		break
	}
	hasChildren := false
	for p := range paths {
		if strings.HasPrefix(p, path+" ") {
			hasChildren = true
		}
	}
	if i < len(toks) && hasChildren && wordRe.MatchString(toks[i]) {
		return fmt.Sprintf("unknown command %q", path+" "+toks[i])
	}
	for _, t := range toks[i:] {
		if strings.HasPrefix(t, "--") {
			flagToks = append(flagToks, t)
		}
	}
	for _, f := range flagToks {
		name, _, _ := strings.Cut(f, "=")
		name = strings.TrimRight(name, ",.")
		found := false
		for _, ok := range paths[path] {
			if ok == name {
				found = true
			}
		}
		if !found {
			return fmt.Sprintf("%q has no flag %s", path, name)
		}
	}
	return ""
}

// CheckKeys compares the backticked keys in the first column of the
// tables in the configuration reference (page, an fs path) with keys.
func CheckKeys(fsys fs.FS, page string, keys []string) ([]string, error) {
	data, err := fs.ReadFile(fsys, page)
	if err != nil {
		return nil, err
	}
	documented := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if m := keyCell.FindStringSubmatch(line); m != nil {
			documented[m[1]] = true
		}
	}
	want := map[string]bool{}
	var problems []string
	for _, k := range keys {
		want[k] = true
		if !documented[k] {
			problems = append(problems, fmt.Sprintf("%s: config key %q is not documented", page, k))
		}
	}
	for k := range documented {
		if !want[k] {
			problems = append(problems, fmt.Sprintf("%s: documented key %q does not exist", page, k))
		}
	}
	return problems, nil
}
