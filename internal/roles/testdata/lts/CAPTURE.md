# lts fixtures: capture record

These are the human-captured inputs for the `scanners/lts` role (#47). An agent worker has no network access and must not invent them.

## endoflife.date API fixtures (`api/`)

- Captured on 2026-11-23 from `GET https://endoflife.date/api/v1/products/<slug>`, every response HTTP 200, and stored as `jq .` output with no other changes.
- Products: `go`, `nodejs`, `python`, `debian`, `ubuntu`, `alpine-linux`, `redis`, `nginx`, `angular`.
- **The Alpine slug is `alpine-linux`, not `alpine`.** `/api/v1/products/alpine` answers `301` to `/api/v1/products/alpine-linux/`, and the scanner treats any non-200 as a failure. `products.json` must map the `alpine` image to `alpine-linux`.
- `make lts-fixtures` (`regen.sh`) regenerates these files. Review every diff by hand, as with cve.

## Starter slugs checked

On 2026-11-23, all 19 starter slugs in #47's `products.json` answered 200 with a non-empty `releases` array, once `alpine` is replaced with `alpine-linux`:

go, nodejs, python, alpine-linux, debian, ubuntu, postgresql, redis, nginx, mysql, mariadb, ruby, php, eclipse-temurin, angular, vue, nextjs, nuxt, electron.

## yq pin (for `scanners/lts/Dockerfile` and the root `Dockerfile`)

- Version: **mikefarah/yq v4.54.3**, published 2026-11-09. It is the newest release at least two weeks old on 2026-11-23.
- SHA-256 values come from the release's own `checksums` file, the SHA-256 column per `checksums_hashes_order`. Each was also checked against the downloaded binary:

| Asset | SHA-256 |
|---|---|
| `yq_linux_amd64` | `129534601871538efe0ba4c5e62a34fb1d5f469bcb975c4fddeb467d82974973` |
| `yq_linux_arm64` | `89f69310b61b68d1245065a1241d1fc843f7a36cd6103ffd77ad987613bd1b67` |

Download URL: `https://github.com/mikefarah/yq/releases/download/v4.54.3/<asset>`.
