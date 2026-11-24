---
role: engineer
status: done
attempt: 1
---
Adds a new `internal/setupcmd` package and an `ard setup` command that runs its checks, then applies the `ard:*` label sync only when the permission check is green. It supports `--check` (exit 2 on drift) and `--dry-run`, and `ard labels sync` stays as an alias. Size M, risk low to medium.

## Open issues
- The shared permission check belongs to #302, which has not landed. This plan gives setup a seam with a minimal default (repository reachable) and does not build the full check. Once #302 lands, a one-line change in `cmd/ard/main.go` swaps it in. A maintainer should confirm this split.
- The `ARD_BOT_LOGIN` variable and the ruleset, merge-settings and workflow checks are outside this issue and left to later parts.
