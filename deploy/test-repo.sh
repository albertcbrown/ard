#!/usr/bin/env bash
# Repository-level checks of ard's own CI/CD files: Jenkinsfile.ci's shape, the GitHub Actions workflows left alone,
# the .ard/ config, `ard config check`, and the docs. Static where the claim is about a file, `go run` for the config
# check. Prints PASS:/FAIL: per case. Exit 0 ok, 1 a case failed, 2 tooling.
# Env: BASE_REF (default origin/main) for the unchanged-workflows check.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2
for t in python3 yq go git; do command -v "$t" >/dev/null 2>&1 || { echo "ERROR: $t is required" >&2; exit 2; }; done
failures=0
pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1${2:+ - $2}" >&2; failures=$((failures + 1)); }
check() { local label="$1"; shift; if "$@"; then pass "$label"; else fail "$label"; fi; }

# ---- Jenkinsfile.ci ---------------------------------------------------------------------------------------------------
# jf <query>: answers questions about the declarative stages of Jenkinsfile.ci (brace-matched, comments stripped).
jf() {
  python3 - "$ROOT/Jenkinsfile.ci" "$@" <<'PY'
import re, sys
src = open(sys.argv[1]).read()
q = sys.argv[2:]
# strip // comments outside strings (good enough for this file: no // inside its strings except URLs in the pod yaml)
code = re.sub(r"(?m)^\s*//.*$", "", src)
def block_after(text, start):
    i = text.index("{", start); depth = 0
    for j in range(i, len(text)):
        if text[j] == "{": depth += 1
        elif text[j] == "}":
            depth -= 1
            if depth == 0: return text[i + 1:j]
    raise SystemExit("unbalanced")
stages_body = block_after(code, code.index("stages {"))
stages = []
for m in re.finditer(r"stage\('([^']+)'\)", stages_body):
    stages.append((m.group(1), block_after(stages_body, m.end())))
names = [n for n, _ in stages]
body = dict(stages)
pipeline_body = block_after(code, code.index("pipeline {"))
cmd = q[0]
if cmd == "names":
    print(" ".join(names))
elif cmd == "body":
    print(body.get(q[1], ""))
elif cmd == "has-post":
    print("yes" if re.search(r"(?m)^\s*post\s*\{", pipeline_body) else "no")
elif cmd == "agent":
    print(block_after(pipeline_body, pipeline_body.index("agent {")))
elif cmd == "all":
    print(code)
PY
}

runs_make() {
  local want="lint:make lint test:make test build:make build chart-test:make chart-test" pair stage target
  [ "$(jf names)" = "lint test build chart-test publish release" ] || { echo "stages: $(jf names)" >&2; return 1; }
  for pair in $want; do
    stage="${pair%%:*}"; target="${pair#*:}"
    jf body "$stage" | grep -q "$target" || { echo "stage $stage does not run '$target'" >&2; return 1; }
  done
  # sh steps fail the build on a non-zero exit; nothing may swallow one
  ! jf all | grep -Eq '(returnStatus|\|\| *true|catchError|unstable|warnError|set \+e)' || { echo "a step swallows failures" >&2; return 1; }
}
check "Jenkinsfile.ci runs make lint test build chart-test" runs_make

no_creds() { ! jf all | grep -Eq '(withCredentials|credentials\(|sshagent|usernamePassword|string\(credentialsId|file\(credentialsId|sshUserPrivateKey|withVault|withAWS|docker\.withRegistry)'; }
check "Jenkinsfile.ci binds no credentials" no_creds

# The pod comes only from the Jenkins pod template `ard-ci` (defined with the job, which mounts no service account
# token): one agent block, no inline yaml or yamlFile, no stage-level agent.
pod_template_only() {
  local a; a="$(jf agent | tr -s ' \n' ' ')"
  [ "$a" = " kubernetes { inheritFrom 'ard-ci' defaultContainer 'golang' } " ] || { echo "agent: $a" >&2; return 1; }
  [ "$(jf all | grep -c 'agent ')" = "1" ] || return 1
  ! jf all | grep -Eq "(yaml |yamlFile|podTemplate|serviceAccount|automountServiceAccountToken)"
}
check "Jenkinsfile.ci uses only the ard-ci pod template" pod_template_only

starts_build() {
  local b; b="$(jf body publish)"
  printf '%s' "$b" | grep -Eq "when \{ *branch 'main' *\}" || return 1
  printf '%s' "$b" | grep -q "build job: '/ard-build'" || return 1
  printf '%s' "$b" | grep -q "string(name: 'GIT_COMMIT', value: commit)" || return 1
  printf '%s' "$b" | grep -q "git rev-parse HEAD" || return 1
  [ "$(jf all | grep -c "ard-build")" = "1" ] || return 1          # started from this stage only
  [ "$(jf names | tr ' ' '\n' | grep -n '^publish$' | cut -d: -f1)" -gt 4 ] || return 1   # after the four checks
  [ "$(jf has-post)" = "no" ]                                         # not from a post block that runs on failure
}
check "Jenkinsfile.ci starts ard-build with GIT_COMMIT on main success only" starts_build

starts_release() {
  local b; b="$(jf body release)"
  printf '%s' "$b" | grep -Fq "when { tag pattern: '^v(0|[1-9][0-9]*)\\\\.(0|[1-9][0-9]*)\\\\.(0|[1-9][0-9]*)\$', comparator: 'REGEXP' }" || return 1
  printf '%s' "$b" | grep -q "build job: '/ard-release'" || return 1
  printf '%s' "$b" | grep -q "string(name: 'TAG', value: env.TAG_NAME)" || return 1
  [ "$(jf all | grep -c "ard-release")" = "1" ] || return 1
  # the same pattern in Groovy decides; check it on the plan's examples
  python3 - <<'PY'
import re
pat = re.compile(r"^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
ok = {"v0.1.0": True, "v1.2.3": True, "v10.0.1": True, "v1.2": False, "v1.2.3-rc1": False, "v01.2.3": False, "1.2.3": False}
raise SystemExit(0 if all(bool(pat.match(k)) == v for k, v in ok.items()) else 1)
PY
}
check "Jenkinsfile.ci starts ard-release with TAG on a release tag only" starts_release

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
claude_ok() {
  local layout; layout="$(awk '/^## Layout$/{f=1;next} /^## /{f=0} f' CLAUDE.md)"
  printf '%s' "$layout" | grep -Eq '^deploy/argocd/ ' && printf '%s' "$layout" | grep -Eq '^Dockerfile ' && printf '%s' "$layout" | grep -Eq '^Jenkinsfile\.ci '
}
check "CLAUDE.md lists deploy/ and Dockerfile" claude_ok

[ "$failures" -eq 0 ] || { echo "$failures case(s) failed" >&2; exit 1; }
