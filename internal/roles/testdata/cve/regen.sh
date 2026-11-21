#!/bin/sh
# Regenerates the REAL fixture cases (both_tools, osv_only, not_called,
# nested_module, scoped_npm) from real tool runs. Run it inside the pinned
# image so the tool versions are exactly the Dockerfile's:
#
#   make cve-fixtures        # from the repo root; needs Docker and network
#
# (it runs: docker run ... -v internal/roles:/roles -w /roles IMAGE
#  sh testdata/cve/regen.sh). Needs osv-scanner, govulncheck, go and jq on
# PATH, plus egress to proxy.golang.org, api.osv.dev and vuln.go.dev.
#
# Per case it builds a tiny module (or lockfile) under a fixed temp root, runs
# osv-scanner with the flags `scan` uses, runs govulncheck per Go module, and
# writes osv.json, gv.jsonl and want.jsonl next to this script.
#
# Edits to real output (the only ones):
#   - osv.json: the temp root in results[].source.path becomes @ROOT@
#     (the tests turn it into /R); pretty-printed with `jq .`.
#   - gv.jsonl: each module's govulncheck messages are trimmed to the `config`
#     message, every `finding`, and the `osv` messages a finding references.
#     `progress`, `SBOM` and unreferenced `osv` messages (~190 of them, 590 KB;
#     merge.jq never reads them) are dropped.
#   - want.jsonl: merge.jq output, exactly as the tests' runMerge produces it.
#     Review every diff by hand: the golden test cannot tell a correct change
#     from a regression.
# The hand-written synthetic cases are never touched; see cve_test.go.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
merge="$here/../../scanners/cve/merge.jq"
work=/tmp/ard-cve-fixtures
GOTOOLCHAIN=local GOFLAGS=-mod=mod GOWORK=off
export GOTOOLCHAIN GOFLAGS GOWORK

rm -rf /tmp/ard-cve-fixtures
mkdir -p "$work"

# mkmod DIR VARIANT: a Go module requiring the vulnerable x/text v0.3.7.
# called: main reaches language.ParseAcceptLanguage; notcalled: only language.English.
mkmod() {
  mkdir -p "$1"
  printf 'module example.com/vulnfixture\n\ngo 1.21\n\nrequire golang.org/x/text v0.3.7\n' > "$1/go.mod"
  if [ "$2" = called ]; then
    printf 'package main\n\nimport "golang.org/x/text/language"\n\nfunc main() { _, _, _ = language.ParseAcceptLanguage("en") }\n' > "$1/main.go"
  else
    printf 'package main\n\nimport "golang.org/x/text/language"\n\nfunc main() { _ = language.English }\n' > "$1/main.go"
  fi
  (cd "$1" && go mod tidy)
}

# want CASE: golden output, the way runMerge in cve_test.go runs merge.jq.
want() {
  d="$here/$1"
  sed 's#@ROOT@#/R#g' "$d/osv.json" > "$work/osv.fix"
  sed 's#@ROOT@#/R#g' "$d/gv.jsonl" > "$work/gv.fix"
  jq -c -n --arg root /R --arg lroot /L --slurpfile osv "$work/osv.fix" --slurpfile gv "$work/gv.fix" \
    -f "$merge" > "$d/want.jsonl"
}

# finish CASE: scan $work/CASE and write the case's three files.
finish() {
  c=$1
  root="$work/$c"
  out="$here/$c"
  mkdir -p "$out"
  rc=0
  (cd "$root" && osv-scanner scan source --recursive --no-call-analysis=all --format json .) > "$root.raw.json" || rc=$?
  case $rc in 0|1) ;; *) echo "regen: osv-scanner exited $rc for $c" >&2; exit 1 ;; esac
  jq --arg r "$root" \
    '.results |= map(.source.path |= (if startswith($r + "/") then "@ROOT@" + .[($r | length):] else . end))' \
    "$root.raw.json" > "$out/osv.json"
  if grep -q "$work" "$out/osv.json"; then echo "regen: temp root leaked into $c/osv.json" >&2; exit 1; fi

  : > "$out/gv.jsonl"
  # Same discovery and order as `scan`.
  (cd "$root" && find . \( -name .git -o -name vendor -o -name node_modules -o -name testdata \) -prune \
    -o -name go.mod -type f -print) | LC_ALL=C sort > "$root.mods"
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    manifest=${f#./}
    (cd "$root/$(dirname -- "$manifest")" && govulncheck -format json ./...) > "$root.gv.raw"
    jq -c -s --arg manifest "$manifest" '
      ([.[] | select(.finding != null) | .finding.osv] | unique) as $ids
      | {manifest: $manifest,
         messages: [.[] | select(.config != null or .finding != null
                                 or (.osv != null and (.osv.id | IN($ids[]))))]}' \
      "$root.gv.raw" >> "$out/gv.jsonl"
  done < "$root.mods"
  want "$c"
}

# both_tools: reachable x/text finding, both tools.
mkmod "$work/both_tools" called
finish both_tools

# osv_only: the same osv-scanner output with no govulncheck data.
mkdir -p "$here/osv_only"
cp "$here/both_tools/osv.json" "$here/osv_only/osv.json"
: > "$here/osv_only/gv.jsonl"
want osv_only

# not_called: x/text is required and imported but the vulnerable symbols are not
# called, so govulncheck reports package- and module-level findings only.
mkmod "$work/not_called" notcalled
finish not_called

# nested_module: a vulnerable module at sub/ under a root module that is also
# vulnerable but does not call the symbol.
mkmod "$work/nested_module" notcalled
mkmod "$work/nested_module/sub" called
finish nested_module

# scoped_npm: a scoped npm package in a package-lock.json; osv-scanner only.
mkdir -p "$work/scoped_npm/web"
cat > "$work/scoped_npm/web/package-lock.json" <<'JSON'
{
  "name": "web",
  "version": "1.0.0",
  "lockfileVersion": 3,
  "requires": true,
  "packages": {
    "": {
      "name": "web",
      "version": "1.0.0",
      "dependencies": {
        "@babel/traverse": "7.22.0"
      }
    },
    "node_modules/@babel/traverse": {
      "version": "7.22.0",
      "resolved": "https://registry.npmjs.org/@babel/traverse/-/traverse-7.22.0.tgz"
    }
  }
}
JSON
finish scoped_npm

rm -rf /tmp/ard-cve-fixtures
