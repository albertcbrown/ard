package redact

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrBadPath is returned by CommitPath for an unusable issue or log name.
var ErrBadPath = errors.New("redact: bad commit path")

// Lowercase only, apart from the UTC timestamp (20060102T150405Z) the executor
// puts in host log names.
var logNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*(?:[0-9]{8}T[0-9]{6}Z)?\.log$`)

// CommitPath returns ".ard/<issue>/logs/" + the base name of hostLog.
func CommitPath(issue int, hostLog string) (string, error) {
	base := filepath.Base(hostLog)
	if issue < 1 || strings.Contains(base, "..") || strings.ContainsAny(base, `/\`) || !logNameRE.MatchString(base) {
		return "", fmt.Errorf("commit path for issue %d, log %q: %w", issue, hostLog, ErrBadPath)
	}
	return fmt.Sprintf(".ard/%d/logs/%s", issue, base), nil
}
