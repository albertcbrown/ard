---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The change adds the embedded starter prompt, its config override, docs and tests as the issue asks. Tests cover role loading, `when` selection, prompt shape and the example blocks. Approved.

## Checks
- Lint: `make lint` runs `go vet ./...`. It stopped on `internal/roles/testdata/docs/skips/src/_x.go:3:8` (parse error in a deliberately broken testdata fixture the change did not touch). It reported nothing on the changed files.
- Tests: `go test ./internal/roles/ ./internal/initcmd/` passes.
- Diff coverage: skipped. The change adds a prompt, YAML and docs plus test code, and the new tests read the prompt and config directly.
