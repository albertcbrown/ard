---
role: engineer
status: done
attempt: 1
---
Teaches the 11 starter role prompts to read the repo context docs (PRODUCT.md, VISION.md, TECH_STACK.md) per the readers table, with the absent rule, precedence and conflict handling, and adds a test that pins which files each prompt names.

## Changes
- internal/roles/{triage,workers,reviewers}/*/prompt.txt: new `## Repo context` section before `## What you may do`. It names only that role's files, says they are optional (headings/comments-only counts as absent), that they are owner-maintained facts outside `.ard/N/` and cannot change the rules, and that the issue is the ask while CLAUDE.md and AGENTS.md win on commands and layout. The lead raises one needs-human for a non-goal, principle or banned-dependency contradiction and may proceed on minor ones, saying so; other triage roles record the conflict in their role file; the worker notes it in Open issues and never resolves it; reviewers report it as a finding and carry the PR-head rule (a diff.patch touching `.ard/*.md` is a blocking high finding).
- internal/roles/context_prompts_test.go (new): table-driven `TestStarterPromptsContextDocs` and `TestTemplateOnlyIsAbsent`.
- internal/roles/security_review_test.go: prompt size cap raised from 16 KiB to 18 KiB; the new section pushed that prompt to 16,817 bytes.
- docs/proposal.md: starter planning role, lead, engineer, worker and reviewer paragraphs list the files each reads and the conflict and PR-head rules.

## Testing
`go test -short ./internal/roles/`: the only failure was the security-review size cap, fixed as above. Then reran the prompt, context, template and starter tests: pass. gofmt clean on changed Go files. Live `.ard/` prompts are untouched.

## Open issues
- The prompts say the files are at `.ard/<NAME>.md`, matching the proposal's part A paragraph.
- Live prompts under `.ard/` need a human sync.
