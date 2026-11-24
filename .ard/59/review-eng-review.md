---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The shared roles repo change matches plan.md and the issue. An omitted ref now follows the shared repo's remote HEAD, and the merge, local-wins rule, failure paths and snapshot naming are tested. Approved.

## Checks
- `go test ./internal/config ./internal/configsrc ./internal/servecmd`: pass.
- `go vet` on the config and configsrc packages: clean.
- Lint: `make lint` was not run, to stay within the budget.
- Diff coverage: not measured. Reading the code, the new paths are exercised by the new tests, including the `master` default-branch case, the full-SHA pin, a moving branch, an unreachable remote, an invalid shared role and a symlink.

## Previous findings
- internal/config/defaults.go (default ref hard-coded to main): resolved. Ref stays empty, `fetchShared` fetches HEAD, and the docs are updated.
- internal/configsrc/shared_test.go (missing master default-branch test): resolved. `TestSharedDefaultBranchMaster` was added.
- internal/config/render.go (per-role origin not shown): still present, low and non-blocking. The plan allowed dropping it.
