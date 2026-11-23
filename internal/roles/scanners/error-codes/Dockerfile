# Pinned tool image for the error-codes scanner. jq 1.8.2, verified 2026-11-21
# against the project's published checksums. The base image is the same
# multi-arch index digest as scanners/cve/Dockerfile (golang:1.27.4-trixie,
# verified 2026-11-23 with `docker buildx imagetools inspect`), so its layers
# are shared; a test keeps both in step. No Go tools are installed.
#
# The image does NOT copy `scan` or `errcodes.jq`: the executor mounts this role
# directory read-only, so the mounted script is always the one that runs.
# Run:  docker run --rm -v "$PWD":/repo:ro -v <role dir>:/role:ro -w /repo IMAGE /role/scan

ARG GO_IMAGE=golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d

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

FROM ${GO_IMAGE}
COPY --from=tools /out/jq /usr/local/bin/jq
# The executor's /tmp is a noexec tmpfs; /var/tmp is writable by any --user.
ENV TMPDIR=/var/tmp
