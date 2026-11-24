---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/roles/lts_test.go"
    line: 322
    severity: low
    blocking: false
    message: |
      The doc comment says `jqRun` but the function is `ltsJQ`, and the comment above `ltsAPI` says `apiFile`. Rename the comments to match the functions.
---
The lts scanner follows the plan: it fails closed, makes no controller code changes, and is covered by golden, hermetic and failure-path tests. The only finding is a stale comment name, so this is approved.

## Checks
- Lint: `make lint` runs `go vet ./...` and `gofmt -l .`. It stops on a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, which this change did not touch. Nothing was reported on the changed files.
- Tests: `go test ./internal/roles/ -run 'Lts|Embedded|FSContents|Executable'` and `go test ./internal/initcmd/` pass, with jq and yq installed. This covers every lts test added.
- Diff coverage: no Go coverage profile was run, because the new logic is in `sh` and `jq` and Go's coverage tooling cannot see it. The hermetic and golden tests exercise `refs.jq` and `eol.jq`: the 90-day and 180-day boundaries, the floor cap, the codename match, truncation, hostile text and the fail-closed exits.
- Not run: the integration tests (they need the network) and the Docker image test.
