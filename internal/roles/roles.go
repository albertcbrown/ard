// Package roles holds the starter role directories that `ard init` (#27)
// writes into a target repo's .ard/. Paths in FS are relative to .ard/
// (for example "scanners/cve/scan"). embed.FS reports every file as 0444, so
// the writer must chmod files named in ExecutableNames to 0755.
package roles

import "embed"

// FS is the embedded starter roles.
//
//go:embed scanners
var FS embed.FS

// ExecutableNames lists base names that must be written 0755.
var ExecutableNames = []string{"scan"}
