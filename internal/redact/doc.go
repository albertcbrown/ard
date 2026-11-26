// Package redact cleans agent output before it is committed.
//
// It is a leaf library: standard library only, no network, and no imports from
// other internal packages. It never calls GitHub, git, the effect layer or the
// committer. The caller owns every side effect (CLAUDE.md invariant 1).
//
// A log is processed in this order, which follows docs/proposal.md ("The .ard/
// contract") and decision #4:
//
//  1. stream the raw log through the known-value redactor (Secrets), which
//     also replaces host paths (PathRule) with placeholders,
//  2. secret-scan the whole redacted log,
//  3. cap it at 1 MiB as the first 32 KiB plus the tail, cut on line
//     boundaries and joined by one marker line,
//  4. re-scan the capped bytes, and
//  5. return a verdict plus the bytes that are ready to commit.
//
// Host paths are replaced before the scan, not after, so the scan and the
// re-scan of the capped bytes see exactly the bytes that get committed. This
// cannot hide a credential: every scan rule anchors on a token prefix that no
// path contains, and a placeholder matches no rule.
//
// One exemption loosens the scan: an aws-access-key match of exactly 20
// characters ending in the suffix "EXAMPLE" (AWS's published placeholder ids)
// is not a finding. It is judged per match, so it cannot hide another key on
// the same line. Redaction is unaffected.
//
// Contract for the close-out (#20) and the reaper (#37):
//
//   - Commit a log only when Redactor.Log returns err == nil and !Flagged, and
//     commit exactly LogResult.Clean at CommitPath(issue, handle.LogPath).
//   - Treat an error from Log exactly like a hit: commit nothing, add
//     ard:needs-human, and comment the rule IDs and line numbers, never text.
//   - Run Redactor.Text on every agent-written file in the commit set
//     (<stage>-<role>.md, plan.md, issue.md) and on the summary comment before posting.
//   - Never post or commit raw log lines. The reaper's "last 50 log lines"
//     comment must come from LogResult.RedactedPath.
//
// The scan fails closed: any scanner or I/O error means nothing is committed.
// Findings carry a rule ID and a position, never the matched text.
package redact
