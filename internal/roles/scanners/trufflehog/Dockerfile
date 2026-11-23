# Pinned tool image for the trufflehog scanner. Versions verified 2026-11-23:
#   trufflehog 3.100.0 (released 2026-11-19), jq 1.8.2.
# The trufflehog tarball checksums come from trufflehog_3.100.0_checksums.txt,
# whose signature was verified on 2026-11-23 with cosign v3.1.4 ("Verified OK"):
#   cosign verify-blob trufflehog_3.100.0_checksums.txt \
#     --certificate trufflehog_3.100.0_checksums.txt.pem \
#     --signature trufflehog_3.100.0_checksums.txt.sig \
#     --certificate-identity-regexp 'https://github\.com/trufflesecurity/trufflehog/' \
#     --certificate-oidc-issuer https://token.actions.githubusercontent.com
# jq's checksums are the ones in scanners/cve/Dockerfile.
# The base is the same golang:1.27.4-trixie index digest as cve and the ard
# image (root Dockerfile), so the layers are shared.
#
# LICENCE: trufflehog is AGPL-3.0. It is downloaded into this role's image only;
# the binary is NOT in the published ard image.
#
# The image does NOT copy `scan` or `findings.jq`: the executor mounts this role
# directory read-only, so the mounted script is always the one that runs.
# Run:  docker run --rm -v "$PWD":/repo:ro -v <role dir>:/role:ro -w /repo IMAGE /role/scan

ARG GO_IMAGE=golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d

FROM ${GO_IMAGE} AS tools
ARG TARGETARCH
ARG TRUFFLEHOG_VERSION=3.100.0
ARG TRUFFLEHOG_SHA256_AMD64=b2d85eba59865e98f45c9fc9f77d2c6d62df529fdd5755af121304ea5a7cecbb
ARG TRUFFLEHOG_SHA256_ARM64=aa61657049229ae523e8455fcbba87dff57b0a2ba6bdb391a38cf75e6d4016dd
ARG JQ_VERSION=1.8.2
ARG JQ_SHA256_AMD64=b1c22172dd303f3be49e935aa56aa48a8b7a46e0bc838b4997d3bb451495870f
ARG JQ_SHA256_ARM64=8b85c817833814ddca00a144c33705546355afccf0cf39b188f3cdb48b852309
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) th_sha="${TRUFFLEHOG_SHA256_AMD64}"; jq_sha="${JQ_SHA256_AMD64}" ;; \
      arm64) th_sha="${TRUFFLEHOG_SHA256_ARM64}"; jq_sha="${JQ_SHA256_ARM64}" ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    mkdir /out; \
    curl -fsSL -o /tmp/th.tgz "https://github.com/trufflesecurity/trufflehog/releases/download/v${TRUFFLEHOG_VERSION}/trufflehog_${TRUFFLEHOG_VERSION}_linux_${TARGETARCH}.tar.gz"; \
    echo "${th_sha}  /tmp/th.tgz" | sha256sum -c -; \
    tar -xzf /tmp/th.tgz -C /out trufflehog; \
    curl -fsSL -o /out/jq "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-${TARGETARCH}"; \
    echo "${jq_sha}  /out/jq" | sha256sum -c -; \
    chmod 0755 /out/trufflehog /out/jq

FROM ${GO_IMAGE}
COPY --from=tools /out/trufflehog /usr/local/bin/trufflehog
COPY --from=tools /out/jq /usr/local/bin/jq
# The executor's /tmp is a noexec tmpfs; keep the temp dir in /var/tmp.
ENV TMPDIR=/var/tmp
