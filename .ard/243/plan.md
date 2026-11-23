# Plan for #243

## Implementation

### Steps
1. New `deploy/agent/Dockerfile`: single-platform image (host arch, built with plain `docker build`). Base on the same trixie image style as the root `Dockerfile` (git must be >= 2.47). Declare `ARG CLAUDE_VERSION=<claude.TestedVersion, currently 2.1.330>` and `ARG CLAUDE_SHA256_AMD64=...` / `ARG CLAUDE_SHA256_ARM64=...`; pick the arch from `TARGETARCH`, download the pinned claude release binary with `curl -fsSL`, verify with `sha256sum -c -`, install to `/usr/local/bin/claude`. Install git, ca-certificates, jq, shellcheck and similar common linters from apt. Do not install `gh`, a git credential helper, or any AGPL tool (#217); no security tools (#205). Run as a non-root user. Header comment says the image is built locally and never published. The worker needs network to look up the real checksums; if it cannot obtain them, it should stop and report instead of inventing values.
2. New `deploy/test-agent-image.sh` (executable, modelled on `deploy/test-image.sh`: `set -uo pipefail`, `pass()`/`fail()` printing `PASS:`/`FAIL:`, exit 0/1/2). Takes the image tag (default `ard-agent:dev`). Asserts `claude --version` contains the pinned version, `git --version`, and each installed linter are present, and that `command -v gh` fails inside the image.
3. `Makefile`: add `agent-image` and `agent-image-test` to `.PHONY`. `agent-image` runs `docker build -t ard-agent:dev deploy/agent` then prints the ID via `docker image inspect --format '{{.Id}}' ard-agent:dev`. `agent-image-test` runs `deploy/test-agent-image.sh`. Neither is added to `test`. Comment both as needing Docker and network.
4. New `internal/agent/claude/agentimage_test.go` with `TestAgentImagePins`: read `../../../deploy/agent/Dockerfile`, parse `ARG CLAUDE_VERSION=` and require it to equal `TestedVersion`; require non-empty 64-hex `CLAUDE_SHA256_AMD64` and `CLAUDE_SHA256_ARM64`; the failure message says a `TestedVersion` bump must update the Dockerfile version and checksums in the same PR. Also assert the Dockerfile has no `gh` install or credential helper line.
5. `docs/proposal.md`: in Delivery add an "Agent image" bullet (built locally with `make agent-image`, tagged `ard-agent:dev`, never published to a public registry or GHCR, not built by `ard-build`). In Configuration, near the `Dockerfile` bullet (line ~407), add a sentence that a role Dockerfile should start `FROM` the local image by digest or ID. Add `deploy/agent/` to nothing else; `deploy/` is already protected.

### Test plan
- `TestAgentImagePins` (`internal/agent/claude/agentimage_test.go`): passes with matching version and two 64-hex checksums; fails if the version differs from `TestedVersion`, a checksum is missing or malformed, or the Dockerfile installs `gh`.
- Command: `make lint test`.
- Manual, by a human with Docker (not run by the worker): `make agent-image` then `make agent-image-test`, paste the PASS lines.
- Edge: unsupported `TARGETARCH` makes the build fail; `gh` absent must be asserted as a failure of `command -v gh`.

### Risk
Medium-low. Docker build and image test cannot run in the worker's environment, so the Dockerfile is unverified until a human runs it. Touches `deploy/`, which is in `merge.protected_paths`, so merge needs human approval. No change to Go runtime code, `.ard/` config or `ard-build`. Supply chain: checksums must come from the vendor's release, not be guessed.

### Size
M. Files: `deploy/agent/Dockerfile` (new), `deploy/test-agent-image.sh` (new), `Makefile`, `internal/agent/claude/agentimage_test.go` (new), `docs/proposal.md`.
