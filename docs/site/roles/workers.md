# Workers

## What it does

A worker implements the plan. It edits code, tests and docs in its own worktree on a `worker/...` branch. The controller then commits the change, runs the tests and opens a draft pull request. When a test run is red the worker gets fix rounds, and when a reviewer asks for changes it gets review rounds.

## Inputs and outputs

- **Inputs:** the issue, its comments, `plan.md`, and on a fix round `test-output.log`.
- **Outputs:** code changes in the worktree and a report, `.ard/<issue#>/worker-<role>.md`.
- A worker may never change `.github/`, `.ard/` (other than its report), secret-shaped files, symlinks, binaries or files over 1 MiB, nor more changed lines than `diff_max_lines`. Any other change ends the job `needs-human`.

## Labels

- `ard:worker:<role>` requests the worker once the plan is complete and approved.
- `ard:review:<role>` is set on the pull request at close-out to request each review.
- `ard:tests:failed` means the worker could not get the tests green.
- `ard:needs-human` stops automation when the worker is blocked or the change is held.

## Starter roles

`engineer`.

## Config overrides

Set these under `overrides` for `workers/<name>` (see [Configuration](../configuration.md)): `tests`, `max_test_fixes`, `max_review_rounds`, `reviewers`, `diff_warn_lines`, `diff_max_lines`, `model`, `timeout`, `sidecar`.
