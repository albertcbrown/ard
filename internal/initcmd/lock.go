package initcmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/albertcbrown/ard/internal/committer"
)

// LockPath is where ard init records the starter hash of every file it wrote.
const LockPath = ".ard/ard-init.lock"

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Lock maps each starter file ard init has written to the sha256 of the
// content it wrote, and records the ard version that wrote it.
type Lock struct {
	Version string            `yaml:"version"`
	Files   map[string]string `yaml:"files"`
}

func hashOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ParseLock reads a lock from main. The file is untrusted and may have been
// edited by hand, so it never fails: unreadable YAML gives an empty Lock, and
// each bad entry is dropped (its file then counts as edited). The warnings say
// what was ignored.
func ParseLock(data []byte) (Lock, []string) {
	l := Lock{Files: map[string]string{}}
	if len(data) == 0 {
		return l, nil
	}
	var raw struct {
		Version any `yaml:"version"`
		Files   any `yaml:"files"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return l, []string{LockPath + " is not valid YAML; every starter file counts as edited"}
	}
	var warns []string
	if v, ok := raw.Version.(string); ok {
		l.Version = v
	} else if raw.Version != nil {
		warns = append(warns, LockPath+": version is not a string; ignored")
	}
	switch m := raw.Files.(type) {
	case nil:
	case map[string]any:
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s, ok := m[k].(string)
			if !ok || !sha256Re.MatchString(s) || committer.CheckInitPath(k) != nil {
				warns = append(warns, fmt.Sprintf("%s: ignoring entry %q", LockPath, k))
				continue
			}
			l.Files[k] = s
		}
	default:
		warns = append(warns, LockPath+": files is not a mapping; ignored")
	}
	return l, warns
}

// Marshal renders the lock with sorted keys.
func (l Lock) Marshal() ([]byte, error) {
	files := l.Files
	if files == nil {
		files = map[string]string{}
	}
	out, err := yaml.Marshal(Lock{Version: l.Version, Files: files})
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", LockPath, err)
	}
	return append([]byte("# Written by ard init: sha256 of each starter file it wrote. Do not edit.\n"), out...), nil
}

// withLock returns old updated with the starter hash of each created or
// updated file. Entries for other files are kept as they were.
func withLock(old Lock, ops []op, version string) Lock {
	n := Lock{Version: old.Version, Files: map[string]string{}}
	for k, v := range old.Files {
		n.Files[k] = v
	}
	for _, o := range ops {
		if o.Act == actCreate || o.Act == actUpdate {
			n.Files[o.File.Path] = hashOf(o.File.Data)
		}
	}
	n.Version = version
	return n
}
