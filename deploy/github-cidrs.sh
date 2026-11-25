#!/usr/bin/env bash
# Prints jobs.githubCIDRs for the ard-serve chart (executor k8s): GitHub's IPv4 ranges for git and web (pre fetches
# the repository over HTTPS from github.com) from https://api.github.com/meta, with the date they were read.
# Human-run; paste the output into the deployment repository's site values. GitHub changes these ranges now and
# then, and a stale list fails closed (pre cannot fetch). Needs curl and jq.
set -euo pipefail
for t in curl jq; do command -v "$t" >/dev/null 2>&1 || { echo "ERROR: $t is required" >&2; exit 2; }; done
meta="$(curl -fsSL --proto '=https' -H 'Accept: application/vnd.github+json' https://api.github.com/meta)"
echo "# from https://api.github.com/meta (git, web; IPv4) on $(date -u +%Y-%m-%d)"
printf 'jobs:\n  githubCIDRs:\n'
printf '%s' "$meta" | jq -r '[.git[], .web[]] | map(select(test("^[0-9.]+/[0-9]+$"))) | unique | .[] | "    - " + .'
