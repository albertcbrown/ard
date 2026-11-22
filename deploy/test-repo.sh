#!/usr/bin/env bash
# Repository-level checks of ard's own CI/CD files: the Makefile contract, the GitHub Actions workflows left alone,
# the .ard/ config, `ard config check`, and the docs. Static where the claim is about a file, `go run` for the config
# check. Prints PASS:/FAIL: per case. Exit 0 ok, 1 a case failed, 2 tooling.
# Env: BASE_REF (default origin/main) for the unchanged-workflows check.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2
for t in make yq go git; do command -v "$t" >/dev/null 2>&1 || { echo "ERROR: $t is required" >&2; exit 2; }; done
failures=0
pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1${2:+ - $2}" >&2; failures=$((failures + 1)); }
check() { local label="$1"; shift; if "$@"; then pass "$label"; else fail "$label"; fi; }

# ---- the CI contract: Makefile targets, no pipeline in this repository ---------------------------------------------
# The ard-ci pipeline is loaded from the deployment repository, so a branch here supplies only these targets.
make_ok() {
  local t
  for t in lint test build chart-test; do
    make -n "$t" >/dev/null 2>&1 || { echo "make $t is missing" >&2; return 1; }
  done
  [ "$(make -n chart-test 2>/dev/null)" = "deploy/test-charts.sh" ]
}
check "Makefile has the lint test build chart-test targets" make_ok
no_jenkinsfile() { [ -z "$(git ls-files --cached --others --exclude-standard -- 'Jenkinsfile*' '**/Jenkinsfile*' | while read -r f; do [ -e "$f" ] && echo "$f"; done)" ]; }
check "ard repo has no Jenkinsfile" no_jenkinsfile

# ---- GitHub Actions left alone ------------------------------------------------------------------------------------------
base="${BASE_REF:-origin/main}"
check "Actions workflows unchanged against origin/main" \
  git diff --quiet "$base" -- .github/workflows/ci.yml .github/workflows/bot-commits.yml

# ---- the soak's .ard/ -----------------------------------------------------------------------------------------------------
check ".ard/scanners/cve has no Dockerfile" test ! -e .ard/scanners/cve/Dockerfile
starter_ok() {
  cmp -s internal/roles/scanners/cve/scan .ard/scanners/cve/scan && cmp -s internal/roles/scanners/cve/merge.jq .ard/scanners/cve/merge.jq \
    && [ -x .ard/scanners/cve/scan ] && [ "$(ls .ard/scanners/cve | sort | paste -sd' ' -)" = "merge.jq scan" ]
}
check ".ard/scanners/cve matches the embedded starter" starter_ok
paths_ok() {
  local want got
  want="$( { yq '.merge.protected_paths[]' internal/roles/config.yaml; printf '%s\n' 'Jenkinsfile*' Dockerfile .dockerignore deploy/; } )"
  got="$(yq '.merge.protected_paths[]' .ard/config.yaml)"
  [ "$got" = "$want" ] || { diff <(echo "$want") <(echo "$got") >&2; return 1; }
  [ "$(printf '%s\n' "$got" | wc -l | tr -d ' ')" = "17" ]
}
check ".ard/config.yaml protected_paths are the init list plus the 4 CI paths" paths_ok
check ".ard/config.yaml intake mode is label" test "$(yq '.intake.mode' .ard/config.yaml)" = "label"

config_ok() {
  local out; out="$(go run ./cmd/ard config check 2>"${TMPDIR:-/tmp}/ard-config-check.$$")" || { cat "${TMPDIR:-/tmp}/ard-config-check.$$" >&2; return 1; }
  [ "$(printf '%s\n' "$out" | yq '[.roles[] | select(.kind == "scanner") | .name] | join(",")')" = "cve" ] || return 1
  [ "$(printf '%s\n' "$out" | yq '.roles[] | select(.name == "cve") | .scan')" = ".ard/scanners/cve/scan" ] || return 1
  [ "$(printf '%s\n' "$out" | yq '.stale.inactive_for + " " + .stale.suppress_for')" = "30d 90d" ]
}
check "ard config check lists cve and stale" config_ok

# ---- docs ---------------------------------------------------------------------------------------------------------------------
P=docs/proposal.md
soak_ok() {
  grep -q 'The Gate 1 soak week does not run on the Mac: it runs on spark as `ard serve --dry-run`' "$P" &&
    grep -Fq '**Where it runs (decided, #5; amended for the soak, [#144](https://github.com/albertcbrown/ard/issues/144)).**' "$P"
}
check "proposal records the soak on spark" soak_ok
check "proposal host-mode paragraph names the spark soak" \
  grep -q 'Until then the Gate 1 dry-run soak on spark (see Rollout and Delivery) runs `ard serve` with the local executor in a pod, and the deterministic cve scanner runs there as a host subprocess' "$P"
delivery_ok() {
  grep -q '^## Delivery$' "$P" || return 1
  local d; d="$(awk '/^## Delivery$/{f=1;next} /^## /{f=0} f' "$P")"
  for w in '**Checks.**' '**Publish.**' '**Deploy.**' '**Release.**' 'ard-ci' 'ard-build' 'deploy/argocd' 'worker/*' 'ghcr.io/albertcbrown/ard'; do
    printf '%s' "$d" | grep -Fq "$w" || { echo "Delivery lacks $w" >&2; return 1; }
  done
}
check "proposal has a Delivery section" delivery_ok
check "proposal says release tags are annotated" \
  grep -Fq 'Release tags are annotated because Jenkins builds only tags at most a day old and dates a lightweight tag by its commit' "$P"
claude_ok() {
  local layout; layout="$(awk '/^## Layout$/{f=1;next} /^## /{f=0} f' CLAUDE.md)"
  printf '%s' "$layout" | grep -Eq '^deploy/argocd/ ' && printf '%s' "$layout" | grep -Eq '^Dockerfile ' && ! printf '%s' "$layout" | grep -q 'Jenkinsfile'
}
check "CLAUDE.md lists deploy/ and Dockerfile" claude_ok

[ "$failures" -eq 0 ] || { echo "$failures case(s) failed" >&2; exit 1; }
