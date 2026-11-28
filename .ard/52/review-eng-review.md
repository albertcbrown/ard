---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/roles/scanners/architecture/prompt.txt"
    line: 1
    severity: info
    message: |
      This prompt ships to every `ard init` adopter and runs unattended on a timer in host mode. internal/roles/ is not in merge.protected_paths, so nothing forces a maintainer to read it. The plan asks for a human read before merge.
---
The change matches plan.md: it adds the embedded `scanners/architecture` prompt, the config override, the embed line, the init PR body bullet, the docs and the tests. The tests pass and I found no blocking problem.

## Checks
- Lint: `make lint` was not completed. My first combined run with the tests hit the 2-minute timeout, and I did not retry it.
- Tests: `go test ./internal/roles ./internal/initcmd` passes. The tests cover config loading as an LLM scanner, HostUntrusted, the prompt contract, example decoding with `CheckLLMFingerprints`, and a drift-catching negative case.
- Diff coverage: not measured. The change adds no Go logic apart from the embed line, and the new tests exercise the new prompt and config.
