---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a starter license-review reviewer role to the embedded role set under internal/roles (prompt, default allowlist, config override), with tests and proposal docs. Reviewer selection by changed paths (#42) has already merged, so nothing blocks this, and the owner has settled every open design question. The engineer turns the agreed plan into implementation steps.

## Routing
- triage:engineer: confirm the implementation steps and test plan against the current code, including the `when.paths` syntax that #42 shipped (doublestar, matched in `internal/config/types.go`).
- worker engineer: new files under `internal/roles/reviewers/license-review/`, the starter `config.yaml` override, Go tests in `internal/roles` and `internal/initcmd`, and doc updates.

## Open issues
- #42 (reviewer selection rules) and #41 (review fix rounds) have both merged. The starter `when:` rule can ship now. Because #41 has landed, the round-2 input the plan defers to a follow-up may belong in this prompt already. The engineer should check this.
- The plan edits CLAUDE.md, which is a protected path, so the PR will wait for a human approval at merge.
- Adding the role to this repository's own `.ard/reviewers/` and `.ard/config.yaml`, and the manual smoke test on a scratch repo, are human follow-ups. The worker cannot do them.
- #43 (security-review) edits the same starter-role lists, so expect trivial merge conflicts.
