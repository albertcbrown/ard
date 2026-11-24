---
role: engineer
status: done
attempt: 1
---
Adds a locally built default ard-agent image (claude pinned to the adapter's tested version, git, linters; no gh, no credential helper), its image test, Makefile targets, a pin-parity unit test and the proposal text. The image is never published.

## Changes
- `deploy/agent/Dockerfile`: trixie-slim, claude 2.1.330 downloaded per architecture and verified with `sha256sum -c`, plus git, jq, make, shellcheck, yamllint, curl; non-root user.
- `deploy/test-agent-image.sh`: asserts the claude version, the tools, `gh` absent and a non-root user, printing PASS:/FAIL:.
- `Makefile`: `agent-image` (builds `ard-agent:dev`, prints the image ID) and `agent-image-test`; neither is in `make test`.
- `internal/agent/claude/agentimage_test.go`: `TestAgentImagePins` checks the Dockerfile version equals `TestedVersion`, both checksums are 64-hex, and no gh or credential helper.
- `docs/proposal.md`: Delivery "Agent image" bullet and a Configuration sentence about `FROM` by digest or ID.

## Testing
`go vet` and `go test ./internal/agent/...` pass, including the new test; `bash -n` on the script passes. A full `go test ./...` did not finish within the 2-minute limit of my first attempt, so I did not run it to completion. Docker is not available to me, so `make agent-image` and `make agent-image-test` were not run.

## Open issues
- A human with Docker must run `make agent-image` and `make agent-image-test` and paste the PASS lines.
- The checksums come from an issue comment by the owner's operator, quoting the vendor's release manifest. I could not re-fetch or verify them, and the manifest signature was not verified.
- The base image `debian:trixie-slim` is not pinned by digest; I could not look one up.
