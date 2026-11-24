#!/usr/bin/env bash
# Checks the locally built ard-agent image (make agent-image): claude at the
# pinned version, git and the linters present, gh absent, not running as root.
# Usage: deploy/test-agent-image.sh [image]   (default ard-agent:dev)
# Needs Docker. Prints PASS:/FAIL: per case. Exit 0 ok, 1 a case failed, 2 tooling.
set -uo pipefail

IMAGE="${1:-ard-agent:dev}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
command -v docker >/dev/null 2>&1 || { echo "ERROR: docker is required" >&2; exit 2; }
docker image inspect "$IMAGE" >/dev/null 2>&1 || { echo "ERROR: image $IMAGE not found; run make agent-image" >&2; exit 2; }

failed=0
pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1"; failed=1; }
run() { docker run --rm --entrypoint "" "$IMAGE" "$@"; }

want="$(sed -n 's/^const TestedVersion = "\(.*\)"/\1/p' "$ROOT/internal/agent/claude/adapter.go")"
[ -n "$want" ] || { echo "ERROR: cannot read claude.TestedVersion" >&2; exit 2; }

out="$(run claude --version 2>&1)"
case "$out" in
  *"$want"*) pass "claude --version reports $want" ;;
  *) fail "claude --version: want $want, got: $out" ;;
esac

for tool in git jq shellcheck yamllint make curl; do
  if run sh -c "command -v $tool" >/dev/null 2>&1; then pass "$tool is on PATH"; else fail "$tool is missing"; fi
done

if run sh -c 'command -v gh' >/dev/null 2>&1; then fail "gh is on PATH"; else pass "gh is not on PATH"; fi

if [ "$(run id -u)" != 0 ]; then pass "runs as a non-root user"; else fail "runs as root"; fi

exit "$failed"
