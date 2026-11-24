#!/usr/bin/env bash
# Builds the root Dockerfile and checks the image:
#   - one build for linux/amd64 + linux/arm64 into an OCI layout: the index lists exactly those two platforms
#   - the native platform loaded into Docker: ard version, the tools at their pinned versions, uid, labels,
#     and the cve scanner run in the image as a plain subprocess (how `ard serve` runs it in a container)
# Needs Docker with buildx and network (base image, module proxy, osv.dev). Prints PASS:/FAIL: per case.
# Exit 0 ok, 1 a case failed, 2 tooling.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILDER="${ARD_IMAGE_TEST_BUILDER:-ard-image-test}"
command -v docker >/dev/null 2>&1 || { echo "ERROR: docker is required" >&2; exit 2; }
docker buildx version >/dev/null 2>&1 || { echo "ERROR: docker buildx is required" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "ERROR: jq is required" >&2; exit 2; }

work="$(mktemp -d)" || { echo "ERROR: mktemp failed" >&2; exit 2; }
tag="ard-image-test:$$"
tag2="ard-image-test:$$-v"
trap 'rm -rf "$work"; docker image rm -f "$tag" "$tag2" >/dev/null 2>&1' EXIT
failures=0
pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1${2:+ - $2}" >&2; failures=$((failures + 1)); }

docker buildx inspect "$BUILDER" >/dev/null 2>&1 || docker buildx create --name "$BUILDER" --driver docker-container >/dev/null \
  || { echo "ERROR: cannot create buildx builder $BUILDER" >&2; exit 2; }

version="$(git -C "$ROOT" describe --tags --always)"
revision="$(git -C "$ROOT" rev-parse HEAD)"
case "$(docker info --format '{{.Architecture}}')" in
  aarch64|arm64) native=linux/arm64 ;;
  x86_64|amd64) native=linux/amd64 ;;
  *) echo "ERROR: unknown docker architecture" >&2; exit 2 ;;
esac

# ---- both platforms, as ard-build builds them ----------------------------------------------------------------------
if ! docker buildx build --builder "$BUILDER" --platform linux/amd64,linux/arm64 \
     --build-arg VERSION="$version" --build-arg REVISION="$revision" \
     --output "type=oci,dest=$work/index.tar" "$ROOT" >"$work/multi.log" 2>&1; then
  tail -30 "$work/multi.log" >&2; echo "ERROR: multi-platform build failed" >&2; exit 1
fi
mkdir "$work/oci" && tar -xf "$work/index.tar" -C "$work/oci"
idx="$work/oci/blobs/$(jq -r '.manifests[0].digest | sub(":"; "/")' "$work/oci/index.json")"
platforms="$(jq -r '[.manifests[] | select(.platform.os != "unknown") | .platform.os + "/" + .platform.architecture] | sort | join(",")' "$idx")"
if [ "$platforms" = "linux/amd64,linux/arm64" ]; then pass "image index has exactly linux/amd64 and linux/arm64"
else fail "image index has exactly linux/amd64 and linux/arm64" "got $platforms"; fi

# ---- native platform, loaded --------------------------------------------------------------------------------------
if ! docker buildx build --builder "$BUILDER" --platform "$native" --load -t "$tag" \
     --build-arg VERSION="$version" --build-arg REVISION="$revision" "$ROOT" >"$work/native.log" 2>&1; then
  tail -30 "$work/native.log" >&2; echo "ERROR: native build failed" >&2; exit 1
fi
run() { docker run --rm "$@"; }

out="$(run "$tag" version 2>&1)"
if [ "$out" = "ard $version" ]; then pass "ard version prints the stamped version"
else fail "ard version prints the stamped version" "got '$out', want 'ard $version'"; fi

tools_ok() {
  run --entrypoint go "$tag" version | grep -q '^go version go1\.27\.4 ' || { echo "go: $(run --entrypoint go "$tag" version)" >&2; return 1; }
  run --entrypoint govulncheck "$tag" -version 2>&1 | grep -q 'govulncheck@v1\.10\.0' || { echo "govulncheck: $(run --entrypoint govulncheck "$tag" -version 2>&1 | head -3)" >&2; return 1; }
  run --entrypoint osv-scanner "$tag" --version 2>&1 | grep -q 'osv-scanner version: 2\.7\.1' || { echo "osv-scanner: $(run --entrypoint osv-scanner "$tag" --version 2>&1 | head -2)" >&2; return 1; }
  [ "$(run --entrypoint jq "$tag" --version)" = "jq-1.8.2" ] || { echo "jq: $(run --entrypoint jq "$tag" --version)" >&2; return 1; }
  run --entrypoint yq "$tag" --version | grep -q 'version v4\.54\.3$' || { echo "yq: $(run --entrypoint yq "$tag" --version)" >&2; return 1; }
  run --entrypoint git "$tag" --version | grep -q '^git version ' || return 1
  [ "$(run --entrypoint sh "$tag" -c 'command -v ard')" = "/usr/local/bin/ard" ] || return 1
}
if tools_ok; then pass "tools on PATH at pinned versions"; else fail "tools on PATH at pinned versions"; fi

uid="$(run --entrypoint id "$tag" -u):$(run --entrypoint id "$tag" -g)"
if [ "$uid" = "65532:65532" ]; then pass "default uid is 65532"; else fail "default uid is 65532" "got $uid"; fi

label() { docker image inspect "$1" --format "{{ index .Config.Labels \"$2\" }}"; }
if [ "$(label "$tag" org.opencontainers.image.revision)" = "$revision" ] && \
   [ "$(label "$tag" org.opencontainers.image.source)" = "https://github.com/albertcbrown/ard" ]; then
  pass "revision and source labels are set"
else fail "revision and source labels are set"; fi

# The cve role as ard serve runs it in a container: a host subprocess in this image, cwd a read-only worktree, HOME writable.
scan_out="$work/scan.out"
if run --tmpfs /home/ard:uid=65532,gid=65532,exec --tmpfs /var/tmp:uid=65532,gid=65532,exec \
     -v "$ROOT:/repo:ro" -w /repo --entrypoint /repo/.ard/scanners/cve/scan "$tag" >"$scan_out" 2>"$work/scan.err"; then
  if [ ! -s "$scan_out" ] || jq -e -s 'all(.[]; type == "object" and has("fingerprint"))' "$scan_out" >/dev/null 2>&1; then
    pass "cve scan runs in the image as a host subprocess"
  else
    fail "cve scan runs in the image as a host subprocess" "stdout is not finding JSON lines: $(head -c 300 "$scan_out")"
  fi
else
  fail "cve scan runs in the image as a host subprocess" "exit $?: $(tail -5 "$work/scan.err")"
fi

# The lts role the same way. It needs network (endoflife.date); empty output and finding lines both pass.
lts_out="$work/lts.out"
if run --tmpfs /home/ard:uid=65532,gid=65532,exec --tmpfs /var/tmp:uid=65532,gid=65532,exec \
     -v "$ROOT:/repo:ro" -w /repo --entrypoint /repo/internal/roles/scanners/lts/scan "$tag" >"$lts_out" 2>"$work/lts.err"; then
  if [ ! -s "$lts_out" ] || jq -e -s 'all(.[]; type == "object" and has("fingerprint"))' "$lts_out" >/dev/null 2>&1; then
    pass "lts scan runs in the image as a host subprocess"
  else
    fail "lts scan runs in the image as a host subprocess" "stdout is not finding JSON lines: $(head -c 300 "$lts_out")"
  fi
else
  fail "lts scan runs in the image as a host subprocess" "exit $?: $(tail -5 "$work/lts.err")"
fi

# The same run on a fixture with a known-vulnerable dependency must report it: empty output cannot pass.
fixture="$ROOT/deploy/testdata/cve-fixture"
fx_out="$work/fixture.out"
if run --tmpfs /home/ard:uid=65532,gid=65532,exec --tmpfs /var/tmp:uid=65532,gid=65532,exec \
     -v "$fixture:/repo:ro" -v "$ROOT/.ard/scanners/cve:/role:ro" -w /repo --entrypoint /role/scan "$tag" \
     >"$fx_out" 2>"$work/fixture.err" \
   && jq -e -s 'length >= 1 and all(.[]; type == "object" and (.fingerprint | startswith("osv:")))
                and any(.[]; .fingerprint | test(":golang.org/x/text$"))' "$fx_out" >/dev/null 2>&1; then
  pass "cve scan in the image reports the fixture's known vulnerability"
else
  fail "cve scan in the image reports the fixture's known vulnerability" "$(head -c 300 "$fx_out") $(tail -3 "$work/fixture.err")"
fi

# ---- a release build: VERSION is the tag ----------------------------------------------------------------------------
if docker buildx build --builder "$BUILDER" --platform "$native" --load -t "$tag2" \
     --build-arg VERSION=v9.8.7 --build-arg REVISION="$revision" "$ROOT" >"$work/v.log" 2>&1; then
  v_out="$(run "$tag2" version 2>&1)"; v_label="$(label "$tag2" org.opencontainers.image.version)"
  if [ "$v_out" = "ard v9.8.7" ] && [ "$v_label" = "v9.8.7" ]; then pass "VERSION build arg stamps ard version and the version label"
  else fail "VERSION build arg stamps ard version and the version label" "version '$v_out', label '$v_label'"; fi
else
  tail -20 "$work/v.log" >&2; fail "VERSION build arg stamps ard version and the version label" "build failed"
fi

[ "$failures" -eq 0 ] || { echo "$failures case(s) failed" >&2; exit 1; }
