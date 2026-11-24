---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/config/defaults.go"
    line: 43
    severity: high
    blocking: true
    message: |
      An omitted roles_repo.ref defaults to the hard-coded "main", but the owner's comment on the issue (2026-11-24) requires following the shared repo's default branch (remote HEAD), so `master` repos work too.
      The same hard-coded default is in `SharedRoles` (internal/config/source.go:52), and docs/proposal.md says "ref defaults to main". Leave Ref empty when omitted, fetch `HEAD` in fetchShared (internal/configsrc/configsrc.go:461), and update the docs.
      Add the requested test: an omitted ref on a shared repo whose default branch is `master` resolves.
  - file: "internal/configsrc/shared_test.go"
    line: 659
    severity: medium
    blocking: false
    message: |
      The owner-requested test for an omitted ref on a `master` default-branch repo is missing. The full-SHA pin is covered in TestSharedRefMoves.
  - file: "internal/config/render.go"
    severity: low
    message: |
      The per-role origin (Role.Shared, plan step 4) was skipped, as the worker's report says. `ard config check` shows roles_repo but not which roles are shared. This is acceptable because the plan allowed dropping it.
---
The roles_repo merge, snapshot naming, failure handling and local-wins rule match the plan and are well tested. It needs changes because the default ref is hard-coded to `main`, against the owner's clarification to follow the shared repo's default branch.

## Checks
- `go test ./internal/config ./internal/configsrc`: pass.
- Lint: `make lint` was not run, to stay within the time budget. The worker reported that it did not finish either.
- Diff coverage: not measured. Reading the code, the added paths are exercised by the new tests. The exception is the default-branch case, which is untested.
