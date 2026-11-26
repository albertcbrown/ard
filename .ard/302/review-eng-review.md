---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/github/connect.go"
    line: 139
    severity: medium
    blocking: false
    message: |
      For a fine-grained PAT the write check rests on the repo response's `permissions` flags, and the extra probes are GETs. A token with read-only issues or pull requests access can pass the startup check and fail later at the first write.
      Consider documenting this limit in the proposal, or checking with a harmless write-capable probe.
  - file: "internal/servecmd/serve.go"
    line: 894
    severity: low
    blocking: false
    message: |
      identityOf covers only its error path in the tests (66.7%), and Connect's unreachable and App paths are partly unexecuted (76-85%). The gaps are minor; consider adding cases for an unreachable API and a failed App repo probe.
---
The change adds the startup connectivity check, PAT support for live runs and the gh fallback as plan.md describes. Tests pass and cover the core paths, so I approve. The two notes are not blocking.

## Checks
- Lint: `make lint` printed only a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, a test fixture the change does not touch. The output was truncated to its tail, so I did not see the full run.
- Tests with coverage: `go test` on github, servecmd, configcmd and merger passes. Coverage is 87.2% for github, 80.3% for servecmd, 90.3% for configcmd and 83.8% for merger. In connect.go, `app` runs at 76.9%, `token` at 84.8% and `unreachable` at 66.7%. Also below 100%: `GhFallback` at 86.7%, `resolveAuth` at 81.8%, `buildDeps` at 77.8% and `identityOf` at 66.7%.
- The self-review case is decided: reviews by ard's own login never count as approval, and the docs and a gates test say so.
