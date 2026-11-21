# Pinned tool image for the cve scanner. Versions verified 2026-11-21:
#   govulncheck v1.10.0 (golang.org/x/vuln), osv-scanner v2.7.1, jq 1.8.2.
# The osv-scanner and jq checksums below come from the projects' own published
# checksum files (osv-scanner_SHA256SUMS, sha256sum.txt) for those releases.
# The base image (golang:1.27.4-bookworm) is pinned by multi-arch index digest,
# verified 2026-11-21 with:
#   docker buildx imagetools inspect golang:1.27-bookworm
# Docker ignores the tag when a digest is present; it is there for readers.
#
# The image does NOT copy `scan` or `merge.jq`: the executor mounts this role
# directory read-only, so the mounted script is always the one that runs.
# Run:  docker run --rm -v "$PWD":/repo:ro -v <role dir>:/role:ro -w /repo IMAGE /role/scan

ARG GO_IMAGE=golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea

FROM ${GO_IMAGE} AS govulncheck
ARG GOVULNCHECK_VERSION=v1.10.0
RUN CGO_ENABLED=0 GOBIN=/out go install golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}

FROM ${GO_IMAGE} AS tools
ARG TARGETARCH
ARG OSV_SCANNER_VERSION=v2.7.1
ARG OSV_SCANNER_SHA256_AMD64=fb7dab19a8bd9bbe5c3189f09d4b7ee1820c9553a23bac202648a56efd2f6ee8
ARG OSV_SCANNER_SHA256_ARM64=983d22fbad9724514e3fcc6acc83b5fa64c5ffa6b0d9f510028f22c1b223ff81
ARG JQ_VERSION=1.8.2
ARG JQ_SHA256_AMD64=b1c22172dd303f3be49e935aa56aa48a8b7a46e0bc838b4997d3bb451495870f
ARG JQ_SHA256_ARM64=8b85c817833814ddca00a144c33705546355afccf0cf39b188f3cdb48b852309
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) osv_sha="${OSV_SCANNER_SHA256_AMD64}"; jq_sha="${JQ_SHA256_AMD64}" ;; \
      arm64) osv_sha="${OSV_SCANNER_SHA256_ARM64}"; jq_sha="${JQ_SHA256_ARM64}" ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    mkdir /out; \
    curl -fsSL -o /out/osv-scanner "https://github.com/google/osv-scanner/releases/download/${OSV_SCANNER_VERSION}/osv-scanner_linux_${TARGETARCH}"; \
    echo "${osv_sha}  /out/osv-scanner" | sha256sum -c -; \
    curl -fsSL -o /out/jq "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-${TARGETARCH}"; \
    echo "${jq_sha}  /out/jq" | sha256sum -c -; \
    chmod 0755 /out/osv-scanner /out/jq

# The final image keeps the Go toolchain: govulncheck needs it to load packages.
FROM ${GO_IMAGE}
COPY --from=govulncheck /out/govulncheck /usr/local/bin/govulncheck
COPY --from=tools /out/osv-scanner /usr/local/bin/osv-scanner
COPY --from=tools /out/jq /usr/local/bin/jq
# A module whose go directive is newer than this image fails govulncheck (fail
# closed): the executor's /tmp is noexec, so a downloaded toolchain could not
# run. Bump the base image instead.
ENV GOTOOLCHAIN=local
