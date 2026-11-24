---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The change follows plan.md. Triage jobs get one fixed `Worker roles:` line built from the resolved config in `AgentCommand`, so it is identical for every triage role. The tests cover each plan case, including the empty-roles case and the worker and review stages.

## Checks
- `make lint`: `go vet` passed. `gofmt -l .` printed a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, a file the change did not touch. No problems reported in the changed files.
- `go test ./internal/agent/... ./internal/agentjob/... ./internal/triage/...`: all packages pass.
- Diff coverage: no profile run. The added lines in `prompt.go` and `command.go` are executed by the new tests, by inspection.
