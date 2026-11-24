---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The log secret scan should stop treating AWS's published example access key id and secret as findings, while every other match stays fail-closed. The change is narrow and sits inside the internal/redact package, with table tests, so only the engineer plan is needed.

## Routing
- triage:engineer: plan the allowlist for AWS's documented example values in the redact rules, keep the scan fail-closed for everything else, cover it with table tests, and check whether redaction (separate from the scan) changes.
- worker engineer: code change in internal/redact with unit tests.

## Open issues
- This loosens a security control. The reviewer should confirm that only the exact documented example values, or a tightly bounded EXAMPLE suffix, are exempt, and that one exempt token cannot hide a real key elsewhere on the same line.
- If gitleaks also runs on logs (internal/redact/gitleaks.go), the plan should say whether its result for these values also needs handling.
