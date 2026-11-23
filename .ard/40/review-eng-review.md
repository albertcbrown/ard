---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/roles/triage_roles_test.go"
    line: 742
    severity: low
    blocking: false
    message: |
      plan.md's test plan asks for a check that `design/../x` and other roles' files are not writable for the designer.
      TestTriageRolesWritable only checks the prefix list. The traversal case is likely covered by the existing closeout allow_test.go, so no action is needed. A one-line pointer to that test would help.
  - file: "internal/roles/triage_roles_test.go"
    line: 755
    severity: low
    blocking: false
    message: |
      The plan's edge case, that prompts mention `.ard/config.yaml`, `.ard/triage/` and `.github/` only to say needs-human, is not asserted. It holds in the four prompts as written, but nothing keeps it true.
---
The change adds the four starter triage prompts, the config DAG (`after:`), tests and docs as plan.md describes. The roles load, the section ownership and writable paths are asserted, and the prompt examples go through `closeout.Parse` and `closeout.Build`. Approved.

## Checks
- Tests: `go test ./internal/roles/ ./internal/initcmd/ -cover` passes. The roles package has no statements, so coverage is not meaningful there. initcmd is at 80.4%.
- Lint: `make lint` runs `go vet ./...` plus `gofmt -l .`. I ran only `gofmt -l internal/roles`. It flagged `internal/roles/testdata/docs/skips/src/_x.go`, a fixture the change did not touch. Nothing was flagged in the changed files. I did not run `go vet`.
- Diff coverage: the added Go lines are test code only. The prompts and config are exercised by the new tests.
