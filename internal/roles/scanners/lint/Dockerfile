# Pinned tool image for the lint scanner. Versions: golangci-lint v2.15.0
# (released 2026-10-16), jq 1.8.2. Verification pending human: the pins below
# were copied from the cve scanner's Dockerfile and have not been rebuilt from
# this file; build the image and run `make lint-scanner-fixtures` before merge.
#
# golangci-lint is built from source with the image's Go, so its Go version
# equals the image's and it supports every module the image can build (release
# binaries built with an older Go refuse newer go directives). Integrity comes
# from the Go module proxy and the checksum database. CGO_ENABLED=0 means a
# repo config's goplugin custom linter cannot load: such a run fails (closed).
#
# The base image (golang:1.27.4-trixie) is the same digest as the cve scanner's.
# Docker ignores the tag when a digest is present; it is there for readers.
#
# BUMP RULE: bump golangci-lint and regenerate every repo's `baseline` in the
# same human PR. Message text changes between versions move fingerprints: open
# issues autoclose, baseline entries stop matching, and new issues are filed.
#
# The image does NOT copy `scan` or `normalize.jq`: the executor mounts this role
# directory read-only, so the mounted script is always the one that runs.
#
# Generate or refresh the committed baseline (an empty file means no baseline),
# from the repo root:
#   docker build -t ard-lint .ard/scanners/lint && docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp --tmpfs /tmp -v "$PWD":/work:ro -v "$PWD/.ard/scanners/lint":/role:ro -w /work ard-lint /role/scan --baseline > .ard/scanners/lint/baseline
# The same command without --baseline and the redirect runs the scan itself.

ARG GO_IMAGE=golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d

FROM ${GO_IMAGE} AS golangci
ARG GOLANGCI_LINT_VERSION=v2.15.0
RUN CGO_ENABLED=0 GOBIN=/out go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}

FROM ${GO_IMAGE} AS tools
ARG TARGETARCH
ARG JQ_VERSION=1.8.2
ARG JQ_SHA256_AMD64=b1c22172dd303f3be49e935aa56aa48a8b7a46e0bc838b4997d3bb451495870f
ARG JQ_SHA256_ARM64=8b85c817833814ddca00a144c33705546355afccf0cf39b188f3cdb48b852309
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) jq_sha="${JQ_SHA256_AMD64}" ;; \
      arm64) jq_sha="${JQ_SHA256_ARM64}" ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    mkdir /out; \
    curl -fsSL -o /out/jq "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-${TARGETARCH}"; \
    echo "${jq_sha}  /out/jq" | sha256sum -c -; \
    chmod 0755 /out/jq

# The final image keeps the Go toolchain: golangci-lint needs it to load packages.
FROM ${GO_IMAGE}
COPY --from=golangci /out/golangci-lint /usr/local/bin/golangci-lint
COPY --from=tools /out/jq /usr/local/bin/jq
# The executor's /tmp is a noexec tmpfs and the caches are large: TMPDIR lives in
# /var/tmp, exec-capable and writable by any --user. GOTOOLCHAIN=local: no
# implicit toolchain switching.
ENV GOTOOLCHAIN=local
ENV TMPDIR=/var/tmp
