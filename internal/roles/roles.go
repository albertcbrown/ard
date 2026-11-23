// Package roles holds the starter .ard/ tree that `ard init` (#27) proposes
// to a target repo. Paths in FS are relative to .ard/ (for example
// "config.yaml" or "scanners/cve/scan"). embed.FS reports every file as 0444,
// so the writer must commit files named in ExecutableNames as 100755.
package roles

import "embed"

// FS is the starter .ard/ tree `ard init` proposes: config.yaml plus the role
// directories (scanners/cve, scanners/docs, scanners/error-codes, triage/lead, triage/engineer, triage/product-manager, triage/architect, triage/designer, triage/documentation-planner, workers/engineer, reviewers/eng-review, reviewers/license-review and reviewers/security-review); paths are relative to .ard/.
//
// scanners/missing-tests is left out until its scan driver exists; re-add it when wiring it in.
//
//go:embed config.yaml reviewers scanners/cve scanners/docs scanners/error-codes triage workers
var FS embed.FS

// ExecutableNames lists base names that must be written 0755.
var ExecutableNames = []string{"scan"}
