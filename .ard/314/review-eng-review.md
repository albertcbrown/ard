---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/tarball/unpack.go"
    line: 180
    severity: low
    blocking: false
    message: |
      A symlink entry whose parent directory has no directory entry or sibling file is created last and fails with a raw ENOENT, not a classified *Error.
      Pack always emits directory entries, so only odd archives reach this. Consider calling root.MkdirAll on the parent before root.Symlink so such archives unpack, or wrap the error so callers see a uniform error type.
  - file: "internal/tarball/errors.go"
    line: 21
    severity: info
    blocking: false
    message: |
      Error() and the over-long-entry truncation in newError are never executed by the tests (0% and 67% coverage). A one-line test of the message format and truncation would cover them.
---
The new tarball package follows plan.md, which moves the swap helper to #317B. Pack never follows symlinks and Unpack is contained through os.Root. The table, fuzz, bomb and symlink-to-secret tests cover the cases the issue names, so I approve.

## Checks
- `make lint`: `go vet ./...` passed. The gofmt step failed on `internal/roles/testdata/docs/skips/src/_x.go`, a testdata file this change does not touch. `gofmt -l internal/tarball` and `go vet ./internal/tarball/` are clean.
- Diff coverage: `go test -short -coverprofile` on `./internal/tarball/` passed with 83.8% of statements covered. The unexecuted lines are mostly error returns from I/O calls, plus the `Error()` method and the long-entry truncation noted above.
