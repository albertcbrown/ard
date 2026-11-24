---
role: eng-review
status: done
attempt: 2
verdict: approve
---
All earlier findings are fixed. The change matches plan.md: the validation, Docker-down and no-fallback paths and the unset case are all covered by tests that pass. Approved.

## Checks
- Lint: `make lint` was not run. The previous round showed it fails on a pre-existing fixture under `internal/roles/testdata`, which this change does not touch. `gofmt -l internal` reports only that fixture.
- Tests: `go test` on internal/executor/..., configcmd, servecmd and config passes.
- Diff coverage: no coverage profile was run. By reading, every added function and branch is exercised: `Prepare` (default image, Dockerfile wins, trusted scanner on host, unset, Docker down), `ensurePulled`, `CheckImageRef`, `HostUntrustedWith`, `runArgv`, and the `serveLoops` startup checks.
- The Docker integration test and the live Mac check are for a human.

## Previous findings
- internal/servecmd/serve.go (startup checks untested): resolved. `TestServeAgentImageValidation` covers an invalid ref, Docker down and a valid ref.
- internal/servecmd/serve.go (probe uses literal `docker`): still present, low and non-blocking. The probe is now an injectable variable and has a comment explaining the shared default.
- internal/executor/local/hostwarn.go (hostwarn and unset-case tests): resolved. `TestHostTextMentionsAgentImage` was added, and the configcmd tests now set `ARD_AGENT_IMAGE=""` and assert that the warning still fires.
