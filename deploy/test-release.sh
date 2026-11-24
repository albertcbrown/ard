#!/usr/bin/env bash
# Local release check (make release-check): validates .goreleaser.yaml, builds a
# snapshot of all four targets and checks the artifacts and the generated cask.
#   - goreleaser check; goreleaser release --snapshot --clean --skip=publish
#   - ard binary for darwin/linux x arm64/amd64 and checksums.txt in dist/
#   - `ard version` of the host binary prints the injected tag (dist/metadata.json)
#   - the cask has no :no_check and only github.com/albertcbrown/ard/releases URLs
#   - brew audit --cask runs only when brew exists; the cask is audited from
#     a path, which brew may refuse outside a tap; that step is skipped when
#     brew is absent and is not run in CI.
# Needs goreleaser v2 and python3. Exit 0 ok, 1 a check failed, 2 tooling.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
for t in goreleaser python3; do
  command -v "$t" >/dev/null 2>&1 || { echo "ERROR: $t is required (goreleaser: https://goreleaser.com/install/)" >&2; exit 2; }
done
# The tap token is only read when publishing; a placeholder keeps templates rendering.
export TAP_GITHUB_TOKEN="${TAP_GITHUB_TOKEN:-snapshot-placeholder}"

fail() { echo "FAIL: $1" >&2; exit 1; }

goreleaser check
goreleaser release --snapshot --clean --skip=publish

[ -f dist/checksums.txt ] || fail "dist/checksums.txt missing"

python3 -I - <<'PY' || exit 1
import json, sys
arts = json.load(open("dist/artifacts.json"))
have = {(a["goos"], a["goarch"]) for a in arts if a.get("type") == "Binary"}
want = {(o, a) for o in ("darwin", "linux") for a in ("arm64", "amd64")}
missing = sorted(want - have)
if missing:
    print("FAIL: missing build targets:", missing, file=sys.stderr)
    sys.exit(1)
PY

tag="$(python3 -I -c 'import json; print(json.load(open("dist/metadata.json"))["tag"])')"
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
bin="$(python3 -I - "$os" "$arch" <<'PY'
import json, sys
os_, arch = sys.argv[1:3]
for a in json.load(open("dist/artifacts.json")):
    if a.get("type") == "Binary" and a["goos"] == os_ and a["goarch"] == arch:
        print(a["path"]); break
PY
)"
[ -n "$bin" ] && [ -x "$bin" ] || fail "no host binary for $os/$arch in dist/"
got="$("$bin" version)"
[ "$got" = "ard $tag" ] || fail "ard version printed '$got', want 'ard $tag'"
echo "PASS: ard version = $got"

cask=dist/homebrew/Casks/ard.rb
[ -f "$cask" ] || fail "generated cask $cask missing"
! grep -q ':no_check' "$cask" || fail "cask uses :no_check"
bad="$(grep -oE 'https?://[^" ]+' "$cask" | grep -v '^https://github.com/albertcbrown/ard/releases/' | grep -v '^https://github.com/albertcbrown/ard$' || true)"
[ -z "$bad" ] || fail "cask has URLs outside the release: $bad"
echo "PASS: cask has sha256 only and release URLs only"

if command -v brew >/dev/null 2>&1; then
  brew audit --cask "$cask" || fail "brew audit --cask"
else
  echo "SKIP: brew not installed, cask not audited"
fi
