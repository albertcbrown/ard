---
role: eng-review
status: done
attempt: 2
verdict: approve
findings:
  - file: "internal/tarball/unpack.go"
    line: 659
    severity: low
    blocking: false
    message: |
      A symlink entry whose parent directory has no directory entry or sibling file is created last and fails with a raw ENOENT, not a classified *Error.
      Pack always emits directory entries, so only odd archives reach this. Call root.MkdirAll on the parent before root.Symlink, or wrap the error.
  - file: "internal/tarball/errors.go"
    line: 84
    severity: info
    blocking: false
    message: |
      Error() and the over-long-entry truncation in newError are never executed by the tests. A one-line test of the message format and truncation would cover them.
---
The change matches plan.md: Pack never follows symlinks, Unpack is contained through os.Root, and the table, fuzz, bomb and symlink-to-secret tests cover the named cases. Nothing blocking has changed since the last round, so I approve.

## Checks
- Lint: gofmt -l and `go vet` on `./internal/tarball/` are clean. I did not re-run the full `make lint`, whose gofmt step failed last round on an untouched testdata file.
- Diff coverage: `go test -short -coverprofile` on `./internal/tarball/` passes with 83.8% coverage. The unexecuted lines are mostly I/O error returns, plus `Error()` and the truncation noted above.

## Previous findings
- internal/tarball/unpack.go (raw ENOENT on a symlink with no parent directory): still present, now at line 659.
- internal/tarball/errors.go (Error() and truncation untested): still present.
