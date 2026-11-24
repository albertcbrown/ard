# Troubleshooting

## Pushes are rejected with GH013

The ruleset on `main` is rejecting ard's commit. Add the GitHub App to the ruleset's bypass list (see [Getting started](getting-started.md)).

## ard complains that git is too old

ard needs git 2.47.0 or newer. An older git fails some fetches with "did not send all necessary objects" (#165). Upgrade git and restart.

## An issue has `ard:executor:failed`

A job died or ran over time. Check the cause in the issue comment and the log under `.ard/<issue#>/logs/`, fix what it names (see [Operations](operations.md)) and remove the label to retry.

## An issue has `ard:needs-human`

An agent or a gate stopped. Read the last comment. Common reasons: no plan on `main`, a change over `diff_max_lines`, or a write outside the allowlist. Fix it, then remove the label.

## An issue has `ard:tests:failed`

The worker could not get `tests` green within `max_test_fixes` rounds. Read the pull request's test output, fix by hand or adjust the plan, and remove the label.

## Nothing happens to my issue

- With the default `label` intake mode, an issue must carry `ard:pending`.
- Check `ard config check` and that `paused` is not true.
- Check for `ard:hold` or `ard:pause`.
- With `require_approval` on, a worker waits for `ard:approved`.
- Run `ard serve --dry-run` and read the log lines.

## The configuration is rejected

`ard config check` names the file and key. Common causes are an unknown key, a duration written in days, or an `intake.label` that is one of ard's own labels.

## Labels are missing or stale

Run `ard labels sync`. It creates and updates labels and reports stale ones, but never deletes them.
