// Package scanner is the controller side of every scanner: backpressure,
// reconcile, scan, diff, group and file (docs/proposal.md, Scanners).
//
// A run loads the scanner's state into memory, computes its plan with pure
// functions (Reconcile, Diff, PlanGroups) and executes the plan through the
// effect layer. GitHub is the truth: every run re-reads the markers of every
// open scanner issue, so a crash between creating an issue and recording it
// heals on the next run instead of filing a duplicate.
//
// Findings come from an Engine: ExecEngine runs a scan executable,
// AgentEngine runs a prompt.txt scanner as an agent job, and Engines routes
// each role to one of them. Every engine's findings pass the pre-filing guard
// (CheckFindings) before anything is filed.
//
// Scanner output (finding titles, bodies, file names) is derived from
// repository content, so it is untrusted: it is neutralised before it reaches
// an issue body, where it could otherwise forge a marker (CLAUDE.md
// invariant 6).
package scanner
