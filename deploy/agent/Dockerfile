# Default ard-agent image: the agent container of a Kubernetes job pod, and the
# image the local executor runs untrusted-input roles in (ARD_AGENT_IMAGE).
# It holds the claude CLI, git, make, the Go toolchain and the starter roles'
# tools (jq, yq, osv-scanner, govulncheck, golangci-lint) plus shellcheck, so
# every starter role runs on it under `executor: k8s` (#312 gap 7 (a)).
#
# Published publicly as ghcr.io/albertcbrown/ard-agent, beside the ard image
# (owner decision on #320): `ard-build` builds it for linux/amd64 and
# linux/arm64, pushes it by digest, and the deploy pin carries its digest
# (`agentImage.digest`) next to the ard image's. Locally, `make agent-image`
# builds this host's platform as ard-agent:dev.
#
# Because it is public it holds NO secrets: no token, key, credential helper,
# gh, ard, or shell profile. The model credential reaches the container only as
# an env var at run time. `make agent-image-test` scans the built filesystem
# and fails on any credential-shaped file or content (deploy/scan-image-secrets.sh).
# No AGPL tools (#217: no trufflehog), no security tools (role images, #205).
#
# Builds for both platforms on a host without QEMU/binfmt, like the root
# Dockerfile: every RUN is in a stage on $BUILDPLATFORM (Go cross-compiles,
# per-arch binaries are downloaded and checked against pinned checksums), and
# the final stage only copies files in.
#
# Runs as 65532:65532 (jobpod.AgentUID, the UID the cluster's admission policy
# expects of the agent) with a read-only root filesystem: everything it writes
# goes to HOME, TMPDIR and /work, which the executor mounts. GOPATH is under
# /tmp so module downloads do not need the root filesystem.
#
# Pins. CLAUDE_VERSION is claude.TestedVersion (internal/agent/claude/adapter.go);
# a bump changes the version and both checksums in the same PR. Checksums are
# from the vendor's release manifest
# (https://downloads.claude.ai/claude-code-releases/<version>/manifest.json),
# captured 2026-11-24 and rechecked 2026-11-25. GO_IMAGE, jq, yq, osv-scanner,
# govulncheck and golangci-lint are the scanner Dockerfiles' pins
# (internal/roles/scanners/{cve,lts,lint}/Dockerfile). shellcheck v0.11.0
# checksums are the release assets' sha256 digests as GitHub reports them
# (gh release view v0.11.0 -R koalaman/shellcheck --json assets), 2026-11-25.
# TestAgentImagePins and TestAgentImageToolPins keep all of these in step.
#
# The base is trixie, not bookworm: git 2.39 breaks the committer's fetches (#163).
#
#   docker buildx build --platform linux/amd64,linux/arm64 deploy/agent

ARG GO_IMAGE=golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS gotools
ARG TARGETOS
ARG TARGETARCH
ARG GOVULNCHECK_VERSION=v1.10.0
ARG GOLANGCI_LINT_VERSION=v2.15.0
# A cross-compiled `go install` lands in bin/<os>_<arch>/, a native one in bin/.
RUN set -eu; mkdir /out; gopath="$(go env GOPATH)"; \
    for pkg in golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION} \
               github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}; do \
      CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go install "$pkg"; \
      name="$(basename "${pkg%@*}")"; \
      f="${gopath}/bin/${TARGETOS}_${TARGETARCH}/${name}"; [ -f "$f" ] || f="${gopath}/bin/${name}"; \
      cp "$f" "/out/${name}"; \
    done

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS tools
ARG TARGETARCH
ARG CLAUDE_VERSION=2.1.330
ARG CLAUDE_SHA256_AMD64=f390eaa0814e6a1bff74cbb47acb74cb9c89d30ced809fe0af5af00c7bfe8ebb
ARG CLAUDE_SHA256_ARM64=6016f90cac783e367fb1aa97c768a2efb94a0128a5b46b70fb3d8fcbbd9dd444
ARG OSV_SCANNER_VERSION=v2.7.1
ARG OSV_SCANNER_SHA256_AMD64=fb7dab19a8bd9bbe5c3189f09d4b7ee1820c9553a23bac202648a56efd2f6ee8
ARG OSV_SCANNER_SHA256_ARM64=983d22fbad9724514e3fcc6acc83b5fa64c5ffa6b0d9f510028f22c1b223ff81
ARG JQ_VERSION=1.8.2
ARG JQ_SHA256_AMD64=b1c22172dd303f3be49e935aa56aa48a8b7a46e0bc838b4997d3bb451495870f
ARG JQ_SHA256_ARM64=8b85c817833814ddca00a144c33705546355afccf0cf39b188f3cdb48b852309
ARG YQ_VERSION=v4.54.3
ARG YQ_SHA256_AMD64=129534601871538efe0ba4c5e62a34fb1d5f469bcb975c4fddeb467d82974973
ARG YQ_SHA256_ARM64=89f69310b61b68d1245065a1241d1fc843f7a36cd6103ffd77ad987613bd1b67
ARG SHELLCHECK_VERSION=v0.11.0
ARG SHELLCHECK_SHA256_AMD64=b7af85e41cc99489dcc21d66c6d5f3685138f06d34651e6d34b42ec6d54fe6f6
ARG SHELLCHECK_SHA256_ARM64=68a8133197a50beb8803f8d42f9908d1af1c5540d4bb05fdfca8c1fa47decefc
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) cl_plat=linux-x64; cl_sha="${CLAUDE_SHA256_AMD64}"; osv_sha="${OSV_SCANNER_SHA256_AMD64}"; jq_sha="${JQ_SHA256_AMD64}"; \
             yq_sha="${YQ_SHA256_AMD64}"; sc_arch=x86_64; sc_sha="${SHELLCHECK_SHA256_AMD64}" ;; \
      arm64) cl_plat=linux-arm64; cl_sha="${CLAUDE_SHA256_ARM64}"; osv_sha="${OSV_SCANNER_SHA256_ARM64}"; jq_sha="${JQ_SHA256_ARM64}"; \
             yq_sha="${YQ_SHA256_ARM64}"; sc_arch=aarch64; sc_sha="${SHELLCHECK_SHA256_ARM64}" ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    mkdir /out /sc; \
    curl -fsSL -o /out/claude "https://downloads.claude.ai/claude-code-releases/${CLAUDE_VERSION}/${cl_plat}/claude"; \
    echo "${cl_sha}  /out/claude" | sha256sum -c -; \
    curl -fsSL -o /out/osv-scanner "https://github.com/google/osv-scanner/releases/download/${OSV_SCANNER_VERSION}/osv-scanner_linux_${TARGETARCH}"; \
    echo "${osv_sha}  /out/osv-scanner" | sha256sum -c -; \
    curl -fsSL -o /out/jq "https://github.com/jqlang/jq/releases/download/jq-${JQ_VERSION}/jq-linux-${TARGETARCH}"; \
    echo "${jq_sha}  /out/jq" | sha256sum -c -; \
    curl -fsSL -o /out/yq "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_${TARGETARCH}"; \
    echo "${yq_sha}  /out/yq" | sha256sum -c -; \
    curl -fsSL -o /sc/sc.tar.gz "https://github.com/koalaman/shellcheck/releases/download/${SHELLCHECK_VERSION}/shellcheck-${SHELLCHECK_VERSION}.linux.${sc_arch}.tar.gz"; \
    echo "${sc_sha}  /sc/sc.tar.gz" | sha256sum -c -; \
    tar -xzf /sc/sc.tar.gz -C /sc; \
    cp "/sc/shellcheck-${SHELLCHECK_VERSION}/shellcheck" /out/shellcheck; \
    chmod 0755 /out/claude /out/osv-scanner /out/jq /out/yq /out/shellcheck
# The agent user, without a RUN on the target platform: passwd and group are
# the base's (the same files on every platform) plus one line each.
RUN set -eu; mkdir /etc-out; \
    cp /etc/passwd /etc-out/passwd; cp /etc/group /etc-out/group; \
    echo 'agent:x:65532:65532:ard agent:/tmp:/usr/sbin/nologin' >>/etc-out/passwd; \
    echo 'agent:x:65532:' >>/etc-out/group

# The final image keeps the Go toolchain: govulncheck and golangci-lint load
# packages with it, and Go roles build and test with it.
FROM ${GO_IMAGE}
ARG CLAUDE_VERSION=2.1.330
ARG REVISION=unknown
COPY --from=tools /etc-out/passwd /etc-out/group /etc/
COPY --from=tools /out/claude /out/osv-scanner /out/jq /out/yq /out/shellcheck /usr/local/bin/
COPY --from=gotools /out/govulncheck /out/golangci-lint /usr/local/bin/
# HOME and TMPDIR are overridden by both executors (k8s: /job/home and /tmp;
# local: /tmp). DISABLE_AUTOUPDATER: the root filesystem is read-only and the
# version is pinned. GOTOOLCHAIN=local: no implicit toolchain downloads.
ENV HOME=/tmp \
    GOPATH=/tmp/go \
    GOTOOLCHAIN=local \
    DISABLE_AUTOUPDATER=1
USER 65532:65532
WORKDIR /work
LABEL org.opencontainers.image.source="https://github.com/albertcbrown/ard" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.version="${CLAUDE_VERSION}" \
      org.opencontainers.image.title="ard-agent" \
      org.opencontainers.image.description="ard's default agent image: claude CLI, git, Go and the starter roles' tools; no credentials"
