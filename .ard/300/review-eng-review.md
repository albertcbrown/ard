---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/worker/run.go"
    line: 496
    severity: medium
    blocking: false
    message: |
      setFetched (redaction, secret scan, drop-on-hit) still has no test. Only parsing and Body rendering are tested.
      Add a unit test where a fetched field is redacted or the scanner reports a hit, and assert jb.fetched is nil.
  - file: "docs/proposal.md"
    severity: low
    blocking: false
    message: |
      The proposal says the worker records fetched inputs under `fetched:` and writes a CAPTURE.md, but `.ard/workers/engineer/prompt.txt` never mentions `fetched` (no change to it in this diff). Update the prompt in a follow-up, or say in the proposal that this is a later part.
---
The change parses and validates `fetched:` strictly, renders it in the PR body from parsed fields only, and updates the proposal as the issue asks. No blocking findings remain, so this is approved. The two findings from the previous round are unchanged.

## Checks
- Lint: not re-run. The diff is unchanged in substance since the last round, when `make lint` reported nothing on the changed files.
- Tests: not re-run (the controller already ran them). Parsing and Body are covered by table tests.
- Diff coverage: `grep` finds no test that calls setFetched.

## Previous findings
- internal/worker/run.go (setFetched has no test): still present.
- .ard/workers/engineer/prompt.txt (prompt does not mention `fetched:`): still present.
