---
role: engineer
status: done
attempt: 1
---
Adds a janitor loop that, once per 30 days, deletes the `.ard/<issue#>/` files of issues closed more than 90 days ago, committing through the shared main committer. This keeps `.ard/` small while git history keeps the audit trail.

## Changes
- `internal/janitor/` (new): the loop. It lists `.ard/` on main, searches closed issues past the cutoff, re-fetches each to catch a reopen, skips PRs and issues labelled `ard:keep` or `ard:hold`, and deletes the issue's files in requests of at most 256 files (stage `janitor`, role `prune`). Per-issue failures are logged and joined into the tick error. Only numbered directories are candidates; the committer's path guard enforces it too. The 30-day gate is in memory.
- `internal/gitx/local.go`: new `ListTree` (recursive file listing at a revision, literal pathspec, path validation like `FileAt`).
- `internal/servecmd/janitor.go` (new) and `serve.go`: wires the loop with the shared main committer and a lister that fetches main into its own ref (`refs/ard/janitor/main`) in the controller clone; `janitor` added to `knownLoops` and registered before the reaper.
- `internal/github/fake/fake.go`: the fake search accepts `closed:<YYYY-MM-DD`.
- `internal/cli/cmd_serve.go`, `docs/proposal.md`, `CLAUDE.md`: loop list and janitor description, layout entry.

## Testing
Ran `make lint` and `go test ./internal/...`; all pass. Added janitor tests (prunes old, keeps recent, open/reopened, non-issue dirs, keep/hold, 300-file chunking, continue after commit error, monthly gate, claims/name) and `TestListTree`. Updated the loop-list tests in servecmd.

## Open issues
- `CLAUDE.md` is a protected path, so a human must approve it.
- Skipping `ard:keep` and `ard:hold` issues goes beyond the issue text; a maintainer may want to confirm.
- A restart causes one extra, harmless scan. The search is capped at 1,000 candidates per pass; the rest are handled in later passes.
