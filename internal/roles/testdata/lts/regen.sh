#!/bin/sh
# Regenerates the real endoflife.date fixtures in api/ (go, nodejs, python,
# debian, ubuntu, alpine-linux, redis, nginx, angular). Run it inside the pinned
# lts image:
#
#   make lts-fixtures        # from the repo root; needs Docker and network
#
# Each response is stored as `jq .` output with no other change. The hand-written
# api/synthetic.json is not used (lts_test.go embeds its synthetic product) and
# is never touched. Review every diff by hand: the golden tests in lts_test.go
# use these files, and a changed capture can change their results.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
for p in go nodejs python debian ubuntu alpine-linux redis nginx angular; do
  curl --proto =https --tlsv1.2 -sS --max-time 30 --retry 2 -f -A ard-lts-scanner \
    "https://endoflife.date/api/v1/products/$p" | jq . > "$here/api/$p.json.new"
  mv "$here/api/$p.json.new" "$here/api/$p.json"
done
