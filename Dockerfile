# ard controller image: the static ard binary plus the cve and lts scanners' tools
# (jq, osv-scanner, govulncheck, yq), so `ard serve` in a container can run both
# roles as host subprocesses.
# Published as ghcr.io/albertcbrown/ard. Public: it must hold no secrets
# (`make image-test` scans its filesystem and fails on any credential).
#
# It is also the Kubernetes job pods' `pre` and `post` image (#320): they run
# `/usr/local/bin/ard job-pre|job-post` directly (not through tini) as
# 65533:65532 with HOME=/ctl/home and a read-only root, so nothing here may
# assume uid 65532 or HOME=/home/ard for them. `pre` copies /usr/local/bin/ard
# into the agent container, so ard must stay static (CGO_ENABLED=0), and it
# fetches with git, so git (>= 2.31, for GIT_CONFIG_COUNT) must stay. The agent
# container's image is deploy/agent/Dockerfile (ghcr.io/albertcbrown/ard/agent).
#
# Builds for both platforms on a host without QEMU/binfmt: every RUN is in a
# stage on $BUILDPLATFORM (Go cross-compiles, per-arch binaries are downloaded
# with checksums), and the final stage only copies files in.
#
# The base is trixie, not bookworm: git 2.39 breaks the committer's fetches (#163).
#
# Tool versions and checksums are the cve and lts scanners'
# (internal/roles/scanners/{cve,lts}/Dockerfile); keep them in step.
#
#   docker buildx build --platform linux/amd64,linux/arm64 \
#     --build-arg VERSION=$(git describe --tags --always) \
#     --build-arg REVISION=$(git rev-parse HEAD) .

ARG GO_IMAGE=golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG GOVULNCHECK_VERSION=v1.10.0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/ard ./cmd/ard
# A cross-compiled `go install` lands in bin/<os>_<arch>/, a native one in bin/.
RUN set -eu; \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
      go install golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}; \
    gopath="$(go env GOPATH)"; f="${gopath}/bin/${TARGETOS}_${TARGETARCH}/govulncheck"; \
    [ -f "$f" ] || f="${gopath}/bin/govulncheck"; \
    cp "$f" /out/govulncheck

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS tools
ARG TARGETARCH
ARG OSV_SCANNER_VERSION=v2.7.1
ARG OSV_SCANNER_SHA256_AMD64=fb7dab19a8bd9bbe5c3189f09d4b7ee1820c9553a23bac202648a56efd2f6ee8
ARG OSV_SCANNER_SHA256_ARM64=983d22fbad9724514e3fcc6acc83b5fa64c5ffa6b0d9f510028f22c1b223ff81
ARG JQ_VERSION=1.8.2
ARG JQ_SHA256_AMD64=b1c22172dd303f3be49e935aa56aa48a8b7a46e0bc838b4997d3bb451495870f
ARG JQ_SHA256_ARM64=8b85c817833814ddca00a144c33705546355afccf0cf39b188f3cdb48b852309
# yq v4.54.3 (mikefarah/yq), checksums from the release checksums file; keep in step with scanners/lts/Dockerfile.
ARG YQ_VERSION=v4.54.3
ARG YQ_SHA256_AMD64=129534601871538efe0ba4c5e62a34fb1d5f469bcb975c4fddeb467d82974973
ARG YQ_SHA256_ARM64=89f69310b61b68d1245065a1241d1fc843f7a36cd6103ffd77ad987613bd1b67
# tini v0.19.0 static, checksums from the release's .sha256sum files.
ARG TINI_VERSION=v0.19.0
ARG TINI_SHA256_AMD64=c5b0666b4cb676901f90dfcb37106783c5fe2077b04590973b885950611b30ee
ARG TINI_SHA256_ARM64=eae1d3aa50c48fb23b8cbdf4e369d0910dfc538566bfd09df89a774aa84a48b9
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) osv_sha="${OSV_SCANNER_SHA256_AMD64}"; jq_sha="${JQ_SHA256_AMD64}"; tini_sha="${TINI_SHA256_AMD64}"; yq_sha="${YQ_SHA256_AMD64}" ;; \
      arm64) osv_sha="${OSV_SCANNER_SHA256_ARM64}"; jq_sha="${JQ_SHA256_ARM64}"; tini_sha="${TINI_SHA256_ARM64}"; yq_sha="${YQ_SHA256_ARM64}" ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    mkdir /out; \
    curl -fsSL -o /out/osv-scanner "https://github.com/google/osv-scanner/releases/download/${OSV_SCANNER_VERSION}/osv-scanner_linux_${TARGETARCH}"; \
    echo "${osv_sha}  /out/osv-scanner" | sha256sum -c -; \
    curl -fsSL -o /out/jq "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-${TARGETARCH}"; \
    echo "${jq_sha}  /out/jq" | sha256sum -c -; \
    curl -fsSL -o /out/yq "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_${TARGETARCH}"; \
    echo "${yq_sha}  /out/yq" | sha256sum -c -; \
    curl -fsSL -o /out/tini "https://github.com/krallin/tini/releases/download/${TINI_VERSION}/tini-static-${TARGETARCH}"; \
    echo "${tini_sha}  /out/tini" | sha256sum -c -; \
    chmod 0755 /out/osv-scanner /out/jq /out/yq /out/tini

# The final image keeps the Go toolchain (govulncheck loads packages with it);
# git and ca-certificates are already in the golang image.
FROM ${GO_IMAGE}
ARG VERSION=dev
ARG REVISION=unknown
COPY --from=build /out/ard /usr/local/bin/ard
COPY --from=build /out/govulncheck /usr/local/bin/govulncheck
COPY --from=tools /out/osv-scanner /usr/local/bin/osv-scanner
COPY --from=tools /out/jq /usr/local/bin/jq
COPY --from=tools /out/yq /usr/local/bin/yq
COPY --from=tools /out/tini /usr/local/bin/tini
# HOME is the pod's PVC (~/.ard state, the repo clone, Go caches). TMPDIR is
# exec-capable for the cve scan's toolchain downloads. GOTOOLCHAIN=local: the
# scan switches toolchains itself.
ENV HOME=/home/ard \
    TMPDIR=/var/tmp \
    GOTOOLCHAIN=local
USER 65532:65532
LABEL org.opencontainers.image.source="https://github.com/albertcbrown/ard" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="GPL-3.0-only" \
      org.opencontainers.image.title="ard"
ENTRYPOINT ["/usr/local/bin/tini", "--", "/usr/local/bin/ard"]
CMD ["--help"]
