#!/usr/bin/env bash
# Builds deploy/agent/Dockerfile and checks the ard-agent image:
#   - one build for linux/amd64 + linux/arm64 into an OCI layout: the index lists exactly those two platforms
#   - the native platform loaded into Docker: claude at claude.TestedVersion, every tool at its Dockerfile pin,
#     gh/ard/trufflehog absent, uid 65532:65532 with a passwd entry, DISABLE_AUTOUPDATER=1, labels
#   - run as the k8s executor runs the agent container: read-only root, uid 65532, no capabilities, HOME on a
#     writable mount, a static ard (as `pre` copies it) on a read-only /ard-bin running `job-exec`
#   - no secrets: the exported filesystem is scanned (internal/imagescan) and any credential-shaped file or
#     content fails the test; a canary image with a planted credential must fail the same scan
# Usage: deploy/test-agent-image.sh [tag]   (default ard-agent:dev; the native build is loaded under this tag)
# Needs Docker with buildx, network, go and jq. Prints PASS:/FAIL: per case. Exit 0 ok, 1 a case failed, 2 tooling.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DF="$ROOT/deploy/agent/Dockerfile"
TAG="${1:-ard-agent:dev}"
BUILDER="${ARD_IMAGE_TEST_BUILDER:-ard-image-test}"
command -v docker >/dev/null 2>&1 || { echo "ERROR: docker is required" >&2; exit 2; }
docker buildx version >/dev/null 2>&1 || { echo "ERROR: docker buildx is required" >&2; exit 2; }
for t in jq go; do command -v "$t" >/dev/null 2>&1 || { echo "ERROR: $t is required" >&2; exit 2; }; done

work="$(mktemp -d)" || { echo "ERROR: mktemp failed" >&2; exit 2; }
canary="ard-agent-canary:$$"
trap 'rm -rf "$work"; docker image rm -f "$canary" >/dev/null 2>&1' EXIT
failures=0
pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1${2:+ - $2}" >&2; failures=$((failures + 1)); }

arg() { sed -n "s/^ARG $1=\(.*\)\$/\1/p" "$DF" | head -1; }
want_claude="$(sed -n 's/^const TestedVersion = "\(.*\)"/\1/p' "$ROOT/internal/agent/claude/adapter.go")"
[ -n "$want_claude" ] || { echo "ERROR: cannot read claude.TestedVersion" >&2; exit 2; }

docker buildx inspect "$BUILDER" >/dev/null 2>&1 || docker buildx create --name "$BUILDER" --driver docker-container >/dev/null \
  || { echo "ERROR: cannot create buildx builder $BUILDER" >&2; exit 2; }
case "$(docker info --format '{{.Architecture}}')" in
  aarch64|arm64) native=linux/arm64 ;;
  x86_64|amd64) native=linux/amd64 ;;
  *) echo "ERROR: unknown docker architecture" >&2; exit 2 ;;
esac
revision="$(cd "$ROOT" && git rev-parse HEAD)"

# ---- both platforms, as ard-build builds them ----------------------------------------------------------------------
if ! docker buildx build --builder "$BUILDER" --platform linux/amd64,linux/arm64 --build-arg REVISION="$revision" \
     --output "type=oci,dest=$work/index.tar" "$ROOT/deploy/agent" >"$work/multi.log" 2>&1; then
  tail -30 "$work/multi.log" >&2; echo "ERROR: multi-platform build failed" >&2; exit 1
fi
mkdir "$work/oci" && tar -xf "$work/index.tar" -C "$work/oci" && rm -f "$work/index.tar"
idx="$work/oci/blobs/$(jq -r '.manifests[0].digest | sub(":"; "/")' "$work/oci/index.json")"
platforms="$(jq -r '[.manifests[] | select(.platform.os != "unknown") | .platform.os + "/" + .platform.architecture] | sort | join(",")' "$idx")"
if [ "$platforms" = "linux/amd64,linux/arm64" ]; then pass "image index has exactly linux/amd64 and linux/arm64"
else fail "image index has exactly linux/amd64 and linux/arm64" "got $platforms"; fi
rm -rf "$work/oci"

# ---- native platform, loaded --------------------------------------------------------------------------------------
if ! docker buildx build --builder "$BUILDER" --platform "$native" --load -t "$TAG" --build-arg REVISION="$revision" \
     "$ROOT/deploy/agent" >"$work/native.log" 2>&1; then
  tail -30 "$work/native.log" >&2; echo "ERROR: native build failed" >&2; exit 1
fi
run() { docker run --rm --entrypoint "" "$TAG" "$@"; }

out="$(run claude --version 2>&1)"
case "$out" in
  "$want_claude "*) pass "claude --version reports $want_claude" ;;
  *) fail "claude --version reports $want_claude" "got: $out" ;;
esac

# Each tool at its Dockerfile pin; the pins themselves are kept equal to the scanners' by TestAgentImageToolPins.
v() { echo "${1#v}"; }
go_want="$(arg GO_IMAGE | sed -n 's/^golang:\([0-9.]*\)-.*/\1/p')"
check_tool() { # label, expected substring, command...
  local label="$1" want="$2"; shift 2
  local got; got="$(run "$@" 2>&1 | head -3)"
  case "$got" in *"$want"*) pass "$label" ;; *) fail "$label" "want '$want', got: $got" ;; esac
}
check_tool "go is $go_want" "go version go$go_want " go version
check_tool "jq is $(arg JQ_VERSION)" "jq-$(arg JQ_VERSION)" jq --version
check_tool "yq is $(arg YQ_VERSION)" "version $(arg YQ_VERSION)" yq --version
check_tool "osv-scanner is $(arg OSV_SCANNER_VERSION)" "osv-scanner version: $(v "$(arg OSV_SCANNER_VERSION)")" osv-scanner --version
check_tool "govulncheck is $(arg GOVULNCHECK_VERSION)" "govulncheck@$(arg GOVULNCHECK_VERSION)" govulncheck -version
check_tool "golangci-lint is $(arg GOLANGCI_LINT_VERSION)" "has version $(v "$(arg GOLANGCI_LINT_VERSION)") " golangci-lint --version
check_tool "shellcheck is $(arg SHELLCHECK_VERSION)" "version: $(v "$(arg SHELLCHECK_VERSION)")" shellcheck --version
check_tool "git is on PATH" "git version 2." git --version
check_tool "make is on PATH" "GNU Make" make --version
check_tool "curl is on PATH" "curl " curl --version

for tool in gh ard trufflehog; do
  if run sh -c "command -v $tool" >/dev/null 2>&1; then fail "$tool is not on PATH" "found $(run sh -c "command -v $tool")"
  else pass "$tool is not on PATH"; fi
done

ids="$(run sh -c 'echo "$(id -u):$(id -g) $(id -un)"')"
if [ "$ids" = "65532:65532 agent" ]; then pass "default user is 65532:65532 (agent)"; else fail "default user is 65532:65532 (agent)" "got $ids"; fi

envs="$(docker image inspect "$TAG" --format '{{json .Config.Env}}')"
if jq -e 'index("DISABLE_AUTOUPDATER=1") != null' <<<"$envs" >/dev/null; then pass "DISABLE_AUTOUPDATER=1 is set"
else fail "DISABLE_AUTOUPDATER=1 is set" "env $envs"; fi

label() { docker image inspect "$TAG" --format "{{ index .Config.Labels \"$1\" }}"; }
if [ "$(label org.opencontainers.image.source)" = "https://github.com/albertcbrown/ard" ] && \
   [ "$(label org.opencontainers.image.revision)" = "$revision" ] && [ "$(label org.opencontainers.image.version)" = "$want_claude" ]; then
  pass "source, revision and version labels are set"
else fail "source, revision and version labels are set"; fi

# ---- the k8s agent container's shape --------------------------------------------------------------------------------
# A static ard for this platform stands in for the one `pre` copies into the ard-bin emptyDir.
mkdir "$work/ard-bin"
if ! (cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="${native#linux/}" go build -o "$work/ard-bin/ard" ./cmd/ard) 2>"$work/gobuild.err"; then
  cat "$work/gobuild.err" >&2; echo "ERROR: cannot build ard for $native" >&2; exit 2
fi
chmod 0755 "$work/ard-bin" "$work/ard-bin/ard"
pod() {
  docker run --rm --entrypoint "" --read-only --user 65532:65532 --cap-drop ALL --security-opt no-new-privileges \
    --tmpfs /tmp:exec,uid=65532,gid=65532 --tmpfs /job:exec,uid=65532,gid=65532 --tmpfs /work:exec,uid=65532,gid=65532 \
    --tmpfs /out:uid=65532,gid=65532 -v "$work/ard-bin:/ard-bin:ro" \
    -e HOME=/job/home -e TMPDIR=/tmp -e GOFLAGS=-buildvcs=false -w /work "$TAG" "$@"
}
out="$(pod sh -c 'mkdir -p "$HOME" && claude --version' 2>&1)"
case "$out" in
  "$want_claude "*) pass "claude runs on a read-only root as 65532 with HOME=/job/home" ;;
  *) fail "claude runs on a read-only root as 65532 with HOME=/job/home" "got: $out" ;;
esac
if pod /ard-bin/ard job-exec --help >"$work/je.out" 2>&1 && grep -q 'job-exec' "$work/je.out"; then
  pass "/ard-bin/ard job-exec --help runs in the agent image"
else fail "/ard-bin/ard job-exec --help runs in the agent image" "$(head -5 "$work/je.out")"; fi
out="$(pod sh -c 'mkdir -p "$HOME" && printf "module m\n\ngo 1.21\n" >go.mod && printf "package main\n\nfunc main() { println(\"built\") }\n" >m.go && go run .' 2>&1)"
if [ "$out" = "built" ]; then pass "go builds on a read-only root (GOPATH, GOCACHE on writable mounts)"
else fail "go builds on a read-only root (GOPATH, GOCACHE on writable mounts)" "got: $out"; fi

# ---- no secrets in the image ----------------------------------------------------------------------------------------
(cd "$ROOT" && go build -o "$work/imagescan" ./deploy/imagescan) || { echo "ERROR: cannot build imagescan" >&2; exit 2; }
scan() { # image -> imagescan's exit code; findings in $work/scan.out
  local cid rc; cid="$(docker create "$1" /none)" || return 2
  docker export "$cid" | "$work/imagescan" >"$work/scan.out" 2>"$work/scan.err"
  rc=$?; docker rm "$cid" >/dev/null 2>&1; return "$rc"
}
scan "$TAG"; rc=$?
if [ "$rc" = 0 ]; then pass "no credential-shaped file or content in the image"
else fail "no credential-shaped file or content in the image" "exit $rc: $(head -20 "$work/scan.out") $(cat "$work/scan.err")"; fi

# The scan must catch a planted credential (fail closed). The values are built here, never stored in the repo.
mkdir -p "$work/canary/home/.ssh"
printf 'https://x-access-token:%s@github.com\n' "gh""s_$(printf 'Ab3%.0s' $(seq 12))" >"$work/canary/home/.git-credentials"
: >"$work/canary/home/.ssh/id_ed25519"
printf 'FROM scratch\nCOPY home/ /home/agent/\n' >"$work/canary/Dockerfile"
if docker build -q -t "$canary" "$work/canary" >/dev/null 2>"$work/canary.err"; then
  scan "$canary"; rc=$?
  if [ "$rc" = 1 ] && grep -q 'github-token' "$work/scan.out" && grep -q 'name:git-credentials' "$work/scan.out" \
     && grep -q 'name:ssh-private-key' "$work/scan.out"; then
    pass "the secret scan fails an image with a planted credential"
  else fail "the secret scan fails an image with a planted credential" "exit $rc: $(cat "$work/scan.out" "$work/scan.err")"; fi
else fail "the secret scan fails an image with a planted credential" "canary build: $(cat "$work/canary.err")"; fi

[ "$failures" -eq 0 ] || { echo "$failures case(s) failed" >&2; exit 1; }
