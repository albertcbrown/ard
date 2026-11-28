# Scanners

## What it does

A scanner finds problems (CVEs, lint, missing tests, docs gaps, secrets) and files issues. It is a directory, `.ard/scanners/<name>/`, holding either an executable `scan` that prints findings as JSON lines or a `prompt.txt` for an LLM scanner. The controller does all dedupe, grouping, filing and closing, so both kinds share one pipeline and no scanner holds a GitHub token.

## Inputs and outputs

- **Output:** one JSON object per line with a fingerprint, a title, a severity and optionally a group, files and a body. The schema is strict; a line that does not fit is an invalid line.
- Scanner text is untrusted. The controller neutralises titles and bodies before filing, and scans every finding for secrets before anything is filed. That check cannot be turned off.
- A finding with the same fingerprint as an open issue is not filed twice.

## Labels

- `ard:scanner:<name>` marks the issue's owning scanner, used for dedupe and backpressure.
- `ard:pending` sends a filed issue to triage. The `route` override can instead file with the scanner label only, or with `ard:needs-human`.

## Starter roles

`architecture` (an LLM scanner), `cve`, `docs`, `error-codes`, `lint`, `missing-tests`, `trufflehog`, `trufflehog-history`.

## Config overrides

Set these under `overrides` for `scanners/<name>` (see [Configuration](../configuration.md)): `every`, `max_open`, `group_size`, `autoclose`, `route`, `timeout`. The global `intervals.scanners` and `global_max_open` apply to all scanners.
