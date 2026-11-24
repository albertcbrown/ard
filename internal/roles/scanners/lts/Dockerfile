# Pinned tool image for the lts scanner. Versions verified 2026-11-23:
#   jq 1.8.2 (same checksums as scanners/cve), yq v4.54.3 (mikefarah/yq,
#   published 2026-11-09). The yq checksums come from the release's own
#   checksums file; see internal/roles/testdata/lts/CAPTURE.md.
# The base image is the same pinned golang image as scanners/cve/Dockerfile, so
# layers are shared and curl, ca-certificates and sh are already there. It is
# pinned by multi-arch index digest; Docker ignores the tag when a digest is
# present; it is there for readers.
#
# The image does NOT copy `scan`: the executor mounts this role directory
# read-only, so the mounted script is always the one that runs.
# Run:  docker run --rm -v "$PWD":/repo:ro -v <role dir>:/role:ro -w /repo IMAGE /role/scan

ARG GO_IMAGE=golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d

FROM ${GO_IMAGE} AS tools
ARG TARGETARCH
ARG JQ_VERSION=1.8.2
ARG JQ_SHA256_AMD64=b1c22172dd303f3be49e935aa56aa48a8b7a46e0bc838b4997d3bb451495870f
ARG JQ_SHA256_ARM64=8b85c817833814ddca00a144c33705546355afccf0cf39b188f3cdb48b852309
ARG YQ_VERSION=v4.54.3
ARG YQ_SHA256_AMD64=129534601871538efe0ba4c5e62a34fb1d5f469bcb975c4fddeb467d82974973
ARG YQ_SHA256_ARM64=89f69310b61b68d1245065a1241d1fc843f7a36cd6103ffd77ad987613bd1b67
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) jq_sha="${JQ_SHA256_AMD64}"; yq_sha="${YQ_SHA256_AMD64}" ;; \
      arm64) jq_sha="${JQ_SHA256_ARM64}"; yq_sha="${YQ_SHA256_ARM64}" ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    mkdir /out; \
    curl -fsSL -o /out/jq "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-${TARGETARCH}"; \
    echo "${jq_sha}  /out/jq" | sha256sum -c -; \
    curl -fsSL -o /out/yq "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_${TARGETARCH}"; \
    echo "${yq_sha}  /out/yq" | sha256sum -c -; \
    chmod 0755 /out/jq /out/yq

FROM ${GO_IMAGE}
COPY --from=tools /out/jq /usr/local/bin/jq
COPY --from=tools /out/yq /usr/local/bin/yq
ENV TMPDIR=/var/tmp
