---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/roles/reviewers/eng-review/prompt.txt"
    line: 87
    severity: low
    blocking: false
    message: |
      The new bullet is slightly ambiguous: it says to skip the checksum re-verification without network, then says "an unverifiable checksum" is a high finding.
      A reviewer without network could read that as a high finding for every fetched file. Say that a checksum skipped for lack of network is noted in ## Checks and is not a finding. The same wording is in security-review/prompt.txt.
---
The change adds the network-access wording to the five starter prompts and the proposal, and pins the key sentences in a new test. It matches issue #301, so I approve it with one low wording note.

## Checks
- Lint: no repository lint entry point was run. The change touches only prompt text, one test file and docs.
- Tests: `go test ./internal/roles -run NetworkPrompt` passes. The pinned phrases exist in the prompts, and a second test shows the matcher fails when a phrase is missing.
- The `fetched:` fields the worker prompt lists (url, date, status, sha256, verified_by, path) match `internal/closeout/rolefile.go`. The "Worker roles:" and "Network:" lines match `internal/agent/claude/prompt.go`.
- Diff coverage: skipped; the change adds no non-test code.
