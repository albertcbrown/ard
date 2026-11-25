---
role: engineer
status: done
attempt: 1
---
Updates the starter role prompts for per-role network access: the worker fetches and records inputs only when its Network line says available, and answers needs-human otherwise; triage and reviewers handle network inputs accordingly.

## Changes
- workers/engineer prompt: conditional Network rule (fetch only what the plan names, verify against publisher checksums, never invent values, `fetched:` plus CAPTURE.md, regen script and Make target); `fetched` listed as an optional key; unavailable means needs-human.
- triage/lead prompt: network-only inputs are not alone a reason for needs-human; uses the Worker roles line.
- triage/engineer prompt: new `### Network inputs` subsection (five subsections now).
- eng-review and security-review prompts: fetched-file check (entry, row, regen target, checksum re-verification when network is available).
- docs/proposal.md: matching sentence in each of the five starter-role paragraphs.
- internal/roles/network_prompts_test.go: pinned phrases per role and a failure-case test of the matcher.

## Testing
`go test -short ./internal/roles/ ./internal/closeout/ ./internal/config/` passes. One earlier run failed because the worker prompt named a repo path ("internal/"), which an existing shape test forbids; I made the wording generic.

## Open issues
- Prompts ship via ard init; a maintainer should read the wording closely.
- Live .ard/ prompts are untouched, as scoped.
- The "unavailable" literal is pinned in the prompt test, not read from the claude package constant (not exported).
