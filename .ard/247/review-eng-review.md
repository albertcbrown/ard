---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "docs/layout.md"
    line: 9
    severity: low
    blocking: false
    message: |
      The new `internal/versioncheck/` entry has one column of padding more than its neighbours, so the descriptions no longer line up. Trim one space.
  - file: "cmd/ard/main.go"
    line: 25
    severity: info
    blocking: false
    message: |
      The worker reports that the manual `make build` and `go install ...@vX.Y.Z` check on a tagged commit was not done, and neither was the parity check against bot-commits.yml. resolveVersion is unit tested, but the end-to-end "Done when" item is unverified. A human should run it after the first tag.
---
The change adds `resolveVersion`, the Makefile describe filter, the versioncheck tests and the docs as plan.md and the issue describe. The new tests pass, so I approve. The CLAUDE.md layout line was skipped because CLAUDE.md points to docs/layout.md, which was updated instead. That is reasonable.

## Checks
- Lint: `make lint` was not run. The worker reports it stops on an unrelated testdata file. I ran `gofmt -l` on `cmd` and `internal/versioncheck`, and it reported nothing.
- Tests: `go test ./internal/versioncheck ./cmd/ard` passes.
- Diff coverage: not measured. `resolveVersion` has table cases for every branch, and the workflow scripts are exercised by the versioncheck tests.
