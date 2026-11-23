# Pinned image for the docs scanner: just the Go toolchain, which builds the
# stdlib-only docscan.go at scan time. The base image (golang:1.27.4-trixie,
# git 2.47) is the same as the cve scanner's, pinned by multi-arch index digest,
# verified 2026-11-23 with:
#   docker buildx imagetools inspect golang:1.27.4-trixie
# Docker ignores the tag when a digest is present; it is there for readers.
#
# The image does NOT copy `scan` or `docscan.go`: the executor mounts this role
# directory read-only, so the mounted files are always the ones that run.
# Run:  docker run --rm -v "$PWD":/repo:ro -v <role dir>:/role:ro -w /repo IMAGE /role/scan

FROM golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d
# The executor's /tmp is a noexec tmpfs and go build executes from its temp dir,
# so TMPDIR is /var/tmp: exec-capable and writable by any --user.
ENV GOTOOLCHAIN=local
ENV TMPDIR=/var/tmp
