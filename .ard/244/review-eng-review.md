---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/servecmd/serve.go"
    line: 214
    severity: high
    blocking: true
    message: |
      The startup checks for ARD_AGENT_IMAGE in serveLoops have no test, and plan.md's test plan names one.
      Nothing exercises an invalid ref failing startup, Docker being down failing startup, or a valid ref logging serve.agent_image. The issue's "invalid image ref is rejected at startup" criterion therefore rests on code no test runs.
      Add a servecmd test that sets ARD_AGENT_IMAGE to a tag and expects an error wrapping executor.ErrImageRef. Add one that sets a valid ref with no working `docker` on PATH and expects the "Docker is not available" error. Making the docker probe injectable would help.
  - file: "internal/servecmd/serve.go"
    line: 219
    severity: low
    message: |
      The startup probe runs the literal `docker` from PATH, while the executor uses Options.DockerBin. Both default to "docker", so this is fine today. If DockerBin ever becomes configurable the two can disagree, so share one value.
  - file: "internal/executor/local/hostwarn.go"
    line: 15
    severity: medium
    message: |
      plan.md's test plan asks for a hostwarn test and for a check that unset ARD_AGENT_IMAGE keeps the warning. Neither was written; the new tests only cover the suppressed case.
      The changed hostHint and HostRoleText wording are untested, and no configcmd or cfgloader test shows the warning still fires when the variable is unset. Add those cases.
---
Changes requested. The implementation matches plan.md and the owner's decision not to mount the clone, but the startup validation of `ARD_AGENT_IMAGE` and the Docker-down check in `ard serve` are untested, and plan.md names that test.

## Checks
- Lint: not run separately. The worker reported that `make lint` fails on a pre-existing fixture under `internal/roles/testdata`, which this change does not touch.
- Tests: `go test` on internal/executor/..., configcmd, servecmd and config passes.
- Diff coverage: no coverage profile was run. By reading, `Prepare`, `ensurePulled`, `CheckImageRef`, `HostUntrustedWith` and `runArgv` are covered. The `serveLoops` block at serve.go:214-224 has no test.
- The Docker integration test and the live Mac check are for a human, as the issue says.
