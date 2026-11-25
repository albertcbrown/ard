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
      The fetched-files bullet still says to skip checksum re-verification without network, then lists "an unverifiable checksum" as a high finding.
      A reviewer without network could read that as a high finding for every fetched file. Say that a checksum skipped for lack of network is noted in ## Checks and is not a finding. The same wording is in security-review/prompt.txt (line 112).
---
The change adds the network-access wording to the starter prompts and the proposal, and pins the key sentences in a test. It matches issue #301, so I approve it. The one low wording note from the previous round is still present.

## Checks
- Lint: no repository lint entry point was run. The change touches only prompt text, one test file and docs.
- Tests: `go test ./internal/roles -run 'NetworkPrompt|PromptShape'` passes.
- Diff coverage: skipped; the change adds no non-test code.

## Previous findings
- internal/roles/reviewers/eng-review/prompt.txt: still present (low, non-blocking).
