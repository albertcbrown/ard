# Reviewers

## What it does

A reviewer critiques a draft pull request and gives a verdict. If it finds blocking issues, the pull request goes back to the worker for another review round. Once every requested review passes and the checks are green, the merger lands the pull request.

## Inputs and outputs

- **Inputs:** the pull request, its issue and `plan.md`.
- **Outputs:** the role's own file, `.ard/<issue#>/<role>.md`, whose verdict becomes the review comment. Reviewers use their own verdicts rather than `done` and `blocked`.

## Labels

- `ard:review:<role>` requests a review on the pull request.
- `ard:review:<role>:changes` sends the pull request back to the worker, up to `max_review_rounds` times.
- `ard:needs-human` stops automation after the last round.
- `ard:needs-approval` is set by the merger when the pull request touches a protected path.

## Starter roles

`eng-review`, `security-review`, `license-review`, `product-manager-review`.

## Config overrides

Set these under `overrides` for `reviewers/<name>` (see [Configuration](../configuration.md)): `model`, `timeout`, `sidecar`. Which reviewers run, and how many rounds, is set on the worker with `reviewers` and `max_review_rounds`. `intervals.reviewers` sets the poll interval.
