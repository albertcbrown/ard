# Default ard-agent image: the claude CLI, git and common linters. It is built
# locally with `make agent-image` (tag ard-agent:dev, this host's platform) and
# is never published: not to a public registry, not to GHCR, and `ard-build`
# does not build it. A role Dockerfile should start FROM it by digest or ID.
#
# No gh, no git credential helper (jobs get no credential), no AGPL tools
# (#217), no security tools (those belong in role images, #205).
#
# CLAUDE_VERSION is claude.TestedVersion (internal/agent/claude/adapter.go).
# A bump must change the version and both checksums in the same PR;
# TestAgentImagePins enforces it. Checksums are from the vendor's release
# manifest (https://downloads.claude.ai/claude-code-releases/<version>/manifest.json),
# captured 2026-11-24 and checked against the downloaded binaries.
#
# The base is trixie, not bookworm: git 2.39 breaks the committer's fetches (#163).

FROM debian:trixie-slim

ARG CLAUDE_VERSION=2.1.330
ARG CLAUDE_SHA256_AMD64=f390eaa0814e6a1bff74cbb47acb74cb9c89d30ced809fe0af5af00c7bfe8ebb
ARG CLAUDE_SHA256_ARM64=6016f90cac783e367fb1aa97c768a2efb94a0128a5b46b70fb3d8fcbbd9dd444

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates curl git jq make shellcheck yamllint && \
    rm -rf /var/lib/apt/lists/*

RUN set -eu; \
    arch="$(dpkg --print-architecture)"; \
    case "$arch" in \
      amd64) platform=linux-x64; sha="$CLAUDE_SHA256_AMD64" ;; \
      arm64) platform=linux-arm64; sha="$CLAUDE_SHA256_ARM64" ;; \
      *) echo "unsupported architecture: $arch" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /usr/local/bin/claude \
      "https://downloads.claude.ai/claude-code-releases/${CLAUDE_VERSION}/${platform}/claude"; \
    echo "${sha}  /usr/local/bin/claude" | sha256sum -c -; \
    chmod 0755 /usr/local/bin/claude

RUN useradd --create-home --uid 65532 --user-group agent
USER 65532:65532
WORKDIR /home/agent
ENV HOME=/home/agent
LABEL org.opencontainers.image.title="ard-agent" \
      org.opencontainers.image.version="${CLAUDE_VERSION}"
