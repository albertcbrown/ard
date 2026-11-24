---
role: engineer
status: done
attempt: 1
---
Adds an `ARD_AGENT_IMAGE` host setting so the local executor runs roles without a Dockerfile in the default image, with startup validation and no fallback to host mode. This round adds the tests the reviewer found missing.

## Changes
- `internal/servecmd/serve.go`: the Docker startup probe is now the injectable `dockerProbe` variable.
- `internal/servecmd/serve_test.go`: `TestServeAgentImageValidation` covers an invalid ref (wraps `ErrImageRef`, probe not run), Docker down ("Docker is not available") and a valid ref logging `serve.agent_image`.
- `internal/executor/local/hostwarn_test.go`: `TestHostTextMentionsAgentImage` covers the changed `hostHint`, `HostRoleText` and `WarnHostRole` wording.
- Earlier rounds (unchanged): executor, config, servecmd and configcmd code, and docs/proposal.md.

## Testing
Ran `go test` on internal/servecmd, internal/executor/..., and internal/configcmd; all pass. `make lint` was not rerun; it fails on a pre-existing fixture under internal/roles/testdata (gofmt reports it) that this change does not touch.

## Review findings
- serve.go:214 (blocking): fixed. Added the three servecmd startup tests via an injectable probe.
- serve.go:219 (low): not changed. The probe and the executor both use "docker", and DockerBin is not configurable today. A comment records the link.
- hostwarn.go:15 (medium): fixed for the wording tests. The unset-keeps-warning cases already exist in `TestCheckWarnsHostUntrusted` (configcmd) and `TestWarnHost` (servecmd cfgloader).

## Open issues
- The Docker integration test and live Mac check are for a human.
