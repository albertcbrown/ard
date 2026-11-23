---
role: engineer
status: needs-human
attempt: 1
---
The request is size XL (well over 1,000 changed lines: a new scanner role with shell and jq, a config key, pipeline label and join changes, a pre-filing secret guard, two Dockerfiles, unit, integration and Docker tests, and docs), so no plan was written. A split into three pull requests is proposed below.

## Open issues
- Proposed split, each part independently buildable and testable:
  1. Scanner-level `route: pending | needs-human` config key: schema, `ard config check` validation, create and join labels in `internal/scanner/pipeline.go`, the join "unclaimed" check, and pipeline tests (stale closer skip, autoclose). Plus the pre-filing secret guard over every finding's title, body and file names (builtin scan, plus gitleaks when enabled), failing the run closed. Size about M to L, risk medium because it changes the shared pipeline for all scanners.
  2. The `scanners/trufflehog` role in `internal/roles/scanners/trufflehog/` (`scan`, `findings.jq`, `Dockerfile`, `exclude-paths`, optional `allow`), with the fake-binary unit tests, canary test and fail-closed tests. It depends on part 1 only for the starter-set override line, so it can follow it. Size L, risk medium because it handles secrets.
  3. Starter-set embedding and `ard init` listings, the integration test (`ARD_TRUFFLEHOG_BIN`), the Docker integration test, the root `Dockerfile` pin, and the proposal and CLAUDE.md updates. The root `Dockerfile` and CLAUDE.md are in `protected_paths`, so this PR needs human approval. Size M to L.
- Enabling the scanner on this repository's own `.ard/` stays a human change, and only after the three URI test literals are built at run time.
- The one-line note on #53 is a GitHub comment that a person or the controller must post.
- Once the split is agreed, file each part as its own issue and re-run triage on them.
