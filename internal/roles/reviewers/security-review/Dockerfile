# Pinned tool image for the security-review aid (docs/proposal.md, Reviewer
# loop, step 3). Tools: gosec, gitleaks and semgrep, plus jq.
#
# Verification pending human: this file was written without network access.
#   - The versions below were not rebuilt from this file. Build it, run the
#     integration test (`go test -tags integration ./internal/roles -run
#     SecurityReviewAid`) and confirm the versions are current before merge.
#   - gosec and gitleaks are built from source with the image's Go (integrity
#     from the Go module proxy and checksum database, as golangci-lint is in the
#     lint scanner's image). Replace the versions by the ones the owner approves.
#   - semgrep is installed from PyPI at a pinned version. Add --require-hashes
#     with a hash-pinned requirements file once the hashes are verified.
#   - jq's checksums are the ones in scanners/lint/Dockerfile.
#
# The image does NOT copy `aid`, `normalize.jq` or `rules/`: the executor
# mounts this role directory read-only, so the mounted files are the ones that
# run. The aid runs under --network none, so semgrep never fetches rules.

ARG GO_IMAGE=golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d

FROM ${GO_IMAGE} AS gotools
ARG GOSEC_VERSION=v2.22.0
ARG GITLEAKS_VERSION=v8.30.0
RUN CGO_ENABLED=0 GOBIN=/out go install github.com/securego/gosec/v2/cmd/gosec@${GOSEC_VERSION} \
 && CGO_ENABLED=0 GOBIN=/out go install github.com/zricethezav/gitleaks/v8@${GITLEAKS_VERSION}

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

# The final image is the default ard-agent image (it holds the claude CLI the
# review agent needs; build it with `make agent-image`), plus the pinned tools.
# Pass the image's ID or digest: docker build --build-arg AGENT_IMAGE=<id> ...
# gosec needs a Go toolchain to load packages, so it is copied from GO_IMAGE.
ARG AGENT_IMAGE=ard-agent:dev
FROM ${AGENT_IMAGE}
ARG SEMGREP_VERSION=1.140.0
USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends python3 python3-venv \
 && rm -rf /var/lib/apt/lists/* \
 && python3 -m venv /opt/semgrep \
 && /opt/semgrep/bin/pip install --no-cache-dir "semgrep==${SEMGREP_VERSION}" \
 && ln -s /opt/semgrep/bin/semgrep /usr/local/bin/semgrep
COPY --from=gotools /usr/local/go /usr/local/go
COPY --from=gotools /out/gosec /usr/local/bin/gosec
COPY --from=gotools /out/gitleaks /usr/local/bin/gitleaks
COPY --from=tools /out/jq /usr/local/bin/jq
USER 65532:65532
# The executor's /tmp is a noexec tmpfs: TMPDIR lives in /var/tmp. GOTOOLCHAIN=local:
# no implicit toolchain switching. SEMGREP_* keep semgrep offline and out of $HOME.
ENV PATH=/usr/local/go/bin:${PATH} \
    GOTOOLCHAIN=local \
    TMPDIR=/var/tmp \
    SEMGREP_SEND_METRICS=off \
    SEMGREP_ENABLE_VERSION_CHECK=0
