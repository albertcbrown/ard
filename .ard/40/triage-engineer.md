---
role: engineer
status: done
attempt: 1
---
Adds four starter triage prompts (product-manager, architect, designer, documentation-planner) with starter config, tests and proposal text, all under internal/roles/, internal/initcmd tests and docs. The plan has nine steps. Size L (could be split into two M pull requests), risk medium because prompt text is security-relevant.

## Open issues
- internal/roles/ is not in the merge protected paths, so the pull request could merge without human approval. A maintainer should add that path to .ard/config.yaml by hand, ideally before merge. Agents may not edit it.
- Copying the roles into .ard/triage/ and the live sample runs need a person, since those are protected paths.
- Step 6 assumes how after: ordering is written in the starter config. The worker should confirm against internal/config/after.go.
