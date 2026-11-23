#!/bin/sh
# Regenerates the REAL fixture cases (basic, nested_module, repo_config_new_only)
# from real golangci-lint runs. Run it inside the pinned image so the tool
# version is exactly the Dockerfile's:
#
#   make lint-scanner-fixtures     # from the repo root; needs Docker
#
# (it runs: docker run ... -v internal/roles:/roles -w /roles IMAGE
#  sh testdata/lint/regen.sh). Needs golangci-lint, go, jq, find and grep on PATH.
#
# Per case it builds a tiny module under a fixed temp root, runs the exact
# golangci-lint command `scan` runs per module, and writes gl.jsonl, nolint.jsonl,
# an empty baseline and want.jsonl next to this script.
#
# Edits to real output (the only ones):
#   - gl.jsonl: the temp root becomes @ROOT@ (the tests turn it into /R), and
#     Report.Linters is dropped (normalize.jq never reads it); pretty fields stay.
#   - want.jsonl: normalize.jq output, exactly as the tests run it, with the
#     version argument "2.15.0". Review every diff by hand: the golden test
#     cannot tell a correct change from a regression.
# The hand-written synthetic cases are never touched; see lint_test.go.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
norm="$here/../../scanners/lint/normalize.jq"
work=/tmp/ard-lint-fixtures
GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOWORK=off
export GOTOOLCHAIN GOFLAGS GOWORK

rm -rf "$work"
mkdir -p "$work"

# mkmod DIR NAME: a module with errcheck (twice, identical), govet printf,
# unused and staticcheck SA4000 findings.
mkmod() {
  mkdir -p "$1"
  printf 'module example.com/%s\n\ngo 1.21\n' "$2" > "$1/go.mod"
  cat > "$1/main.go" <<'GOEOF'
package main

import (
	"fmt"
	"os"
)

func unusedHelper() {}

func main() {
	os.Remove("a")
	os.Remove("b")
	fmt.Printf("%d\n", "x")
	x := len(os.Args)
	if x == x {
		return
	}
}
GOEOF
}

# finish CASE: scan $work/CASE the way `scan` does and write the case's files.
finish() {
  c=$1
  root="$work/$c"
  out="$here/$c"
  mkdir -p "$out"
  : > "$out/gl.jsonl"
  (cd "$root" && find . \( -name .git -o -name vendor -o -name node_modules -o -name testdata \) -prune \
    -o -name go.mod -type f -print) | LC_ALL=C sort > "$root.mods"
  n=0
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    n=$((n + 1))
    manifest=${f#./}
    mdir=$(dirname -- "$manifest")
    (cd "$root/$mdir" && golangci-lint run \
      --path-mode=abs --output.json.path="$root.$n.json" --output.text.path=stderr \
      --show-stats=false --issues-exit-code=0 --max-issues-per-linter=0 --max-same-issues=0 \
      --new=false --new-from-rev= --new-from-merge-base= --new-from-patch= \
      --fix=false --allow-parallel-runners ./...) 2>/dev/null
    jq -c --arg module "$mdir" --arg r "$root" \
      '{module: $module, report: (del(.Report.Linters) | tojson | gsub($r; "@ROOT@") | fromjson)}' \
      "$root.$n.json" >> "$out/gl.jsonl"
  done < "$root.mods"
  if grep -q "$work" "$out/gl.jsonl"; then echo "regen: temp root leaked into $c/gl.jsonl" >&2; exit 1; fi
  : > "$out/nolint.jsonl"
  (cd "$root" && find . \( -name .git -o -name vendor -o -name node_modules -o -name testdata \) -prune \
    -o -name '*.go' -type f -exec grep -lE '//[[:space:]]*nolint' {} + || true) | LC_ALL=C sort > "$root.nl"
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    (cd "$root" && grep -nE '//[[:space:]]*nolint' "$f") \
      | jq -R -c --arg path "${f#./}" 'capture("^(?<line>[0-9]+):(?<text>.*)$") | {path: $path, line: (.line|tonumber), text}' \
      >> "$out/nolint.jsonl"
  done < "$root.nl"
  : > "$out/baseline"
  sed 's#@ROOT@#/R#g' "$out/gl.jsonl" > "$root.gl.fix"
  jq -n -c --arg mode findings --arg root /R --arg lroot /L --arg version 2.15.0 \
    --rawfile baseline "$out/baseline" --slurpfile gl "$root.gl.fix" --slurpfile nolint "$out/nolint.jsonl" \
    -f "$norm" > "$out/want.jsonl"
}

mkmod "$work/basic" basic
finish basic

# nested_module: the same module at sub/ under the root module, so the path
# prefixes differ.
mkmod "$work/nested_module" rootmod
mkmod "$work/nested_module/sub" submod
finish nested_module

# repo_config_new_only: a repo config that would hide every issue in a clean
# checkout (issues.new) and truncate output (max-same-issues). The forced flags
# win, so the output still has every issue.
mkmod "$work/repo_config_new_only" cfgmod
printf 'version: "2"\nissues:\n  new: true\n  max-same-issues: 1\n' > "$work/repo_config_new_only/.golangci.yml"
finish repo_config_new_only
