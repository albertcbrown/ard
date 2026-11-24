---
role: eng-review
status: done
attempt: 2
verdict: changes
findings:
  - file: "docs/site/configuration.md"
    line: 241
    severity: medium
    blocking: true
    message: |
      The documented defaults for `diff_warn_lines` (1000) and `diff_max_lines` (2000) are wrong after the merge of main (#311).
      internal/config/defaults.go:44-45 now sets 2000 and 3000, so the reference misstates the limits a worker is held to.
      Update lines 241 and 242 to 2000 and 3000. The drift test checks key names only, not defaults, so it will not catch this.
  - file: "docs/site/roles.md"
    line: 540
    severity: low
    message: |
      The write allowlist text refers to `diff_max_lines` without a value, so it stays correct. No change needed beyond the defaults fix in configuration.md.
  - file: "internal/config/keys.go"
    line: 8
    severity: low
    message: |
      Still present from the last round. Keys() is a hand-written list. A key added only to the raw.go parser, such as `overrides`, would not fail the test.
  - file: "internal/docscheck/docscheck.go"
    line: 73
    severity: low
    message: |
      Still present. Fenced commands with `\` continuations are not joined, so flags on continuation lines go unchecked.
---
The change matches plan.md and its tests pass. It needs changes for one reason: after the merge of main, the documented `diff_warn_lines` and `diff_max_lines` defaults (1000 and 2000) no longer match the code (2000 and 3000).

## Checks
- Lint: `make lint` was not re-run. In the previous round it reported only a parse error in an untouched fixture. Since then the only change is the merge of main.
- Tests: `go test` on `internal/docscheck`, `internal/cli` and `internal/config` passes.
- `mkdocs build --strict` was skipped because mkdocs is not installed here.

## Previous findings
- internal/config/keys.go: still present (low, non-blocking).
- internal/docscheck/docscheck.go: still present (low, non-blocking).
- mkdocs.yml: still present as info. The CI workflow remains a human step, per plan.md.
