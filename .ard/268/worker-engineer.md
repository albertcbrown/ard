---
role: engineer
status: done
attempt: 1
---
Turns off git's auto gc and auto maintenance for every git command the controller and the local executor run, so a detached `gc --auto` cannot prune `.git/worktrees` outside the worktree lock. This addresses the TestBranchesStress flake.

## Changes
- `internal/gitx/gitx.go`: `run` passes `-c gc.auto=0 -c maintenance.auto=false`.
- `internal/executor/local/git.go`: same options in `execGit.command`.
- `internal/gitx/gitx_test.go`: `TestRunDisablesAutoGC` and the planned `TestWorktreeAdminWithAutoGC` (gc.auto=1, autoDetach, many loose objects, concurrent fetches next to add/remove cycles on distinct lanes).
- `internal/executor/local/misc_test.go`: `TestExecGitDisablesAutoGC` asserts the args.

## Review findings
- `internal/gitx/gitx_test.go:439`: fixed: added `TestWorktreeAdminWithAutoGC` as planned.
- `internal/gitx/gitx.go:58`: not changed: the cause is a hypothesis. The flake did not reproduce locally, so no GIT_TRACE diagnosis was possible. Auto-gc is the leading suspect, not a confirmed cause.
- `internal/executor/local/git.go:35`: fixed: added an args test.

## Testing
- `go test -race ./internal/gitx ./internal/executor/local`: pass.
- `go test -race -count=50 -run TestBranchesStress ./internal/committer`: pass (64 s).

## Open issues
- The root cause is unconfirmed (see above). The new auto-gc test passes with the fix, but I did not check that it fails without it. The backstop retry in plan step 4 was not added.
