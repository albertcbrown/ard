---
role: engineer
status: done
attempt: 1
---
Adds a new starter scanner role, error-codes, made of a POSIX sh script, a jq program and a pinned Dockerfile. It comes with its config entry, tests, an integration test and doc updates, and it needs no controller changes. The plan has nine steps. Size is M to L and risk is medium, because the scanner autocloses issues and so must fail closed.

## Open issues
- CLAUDE.md is a protected path, so the pull request waits for human approval.
- A human must verify the base-image digest and run the Docker integration test once before merge, because worker jobs have no Docker.
- Expect trivial merge conflicts with other pull requests that add starter roles; land them one at a time.
