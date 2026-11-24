---
role: engineer
status: done
attempt: 1
---
Adds three optional repo context docs (PRODUCT.md, VISION.md, TECH_STACK.md under `.ard/`) to the contract, the starter protected paths and `ard init`, which proposes them only when missing on main and never overwrites them.

## Changes
- `internal/roles/{PRODUCT,VISION,TECH_STACK}.md`: embedded templates with headings, HTML-comment guidance, 150-line size guidance and a `Last reviewed: YYYY-MM-DD` line; added to `//go:embed`.
- `internal/roles/config.yaml`: the three explicit paths in `merge.protected_paths` (20 entries, no glob).
- `internal/committer/init.go`: `CheckInitPath` accepts the three names.
- `internal/initcmd`: exported `ContextPaths()`; `plan` treats a context file present on main in any form as unchanged (no conflict, even with force); the snapshot also exports them; the validation overlay uses only changed files; the PR body tells adopters to add the paths to `merge.protected_paths` in an existing config.
- `docs/proposal.md`: contract tree and a "Repo context docs" paragraph, protected-path list, config example, Guardrails and `ard init` paragraph.

## Testing
`go test ./...` passes. Added or updated: `CheckCodePath` and `CheckInitPath` cases, `TestFSContents`, new `TestContextTemplates`, `TestDefaultProtectedPaths` (20 entries, context paths protected, no glob), `starterTree`, `TestPlanContextFiles`, `TestInitContextFiles` (all/none/partial present, force on and off) and the PR body test.

## Open issues
- The #291 design was not available to me, so the template headings and the precedence, reviewer and conflict wording in `docs/proposal.md` are my own reading of the issue; check them against #291.
- This repository's own `.ard/config.yaml` is unchanged; a maintainer must add the three paths by hand.
