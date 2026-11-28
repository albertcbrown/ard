---
role: eng-review
status: done
attempt: 2
verdict: approve
findings:
  - file: "internal/roles/scanners/architecture/prompt.txt"
    line: 1
    severity: info
    message: |
      This prompt ships to every `ard init` adopter and runs unattended on a timer in host mode. internal/roles/ is not in merge.protected_paths, so nothing forces a maintainer to read it. The plan asks for a human read before merge.
---
The change matches plan.md: it adds the embedded `scanners/architecture` prompt, the config override, the embed line, the init PR body bullet, docs and tests. I found no blocking problem. Approved.

## Checks
- Tests: `go test ./internal/roles ./internal/initcmd ./internal/config` passes. They cover config loading as an LLM scanner, HostUntrusted, the prompt contract, example decoding with `CheckLLMFingerprints`, a drift-catching negative case, the embedded file list and the init PR body.
- Lint: `make lint` stopped with `internal/roles/testdata/docs/skips/src/_x.go:3:8: expected ')', found 'EOF'`. That fixture is not touched by the change, so I treat it as pre-existing. I did not get a clean lint result for the changed files.
- Diff coverage: not measured. The change adds no Go logic apart from the embed line, and the new tests exercise the new prompt and config.

## Previous findings
- internal/roles/scanners/architecture/prompt.txt (info, human read before merge): still present. It needs no action from the worker.
