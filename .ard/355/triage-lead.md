---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The log secret scan should stop reporting AWS's published placeholder access key id and secret as findings, and every other match should still fail closed. The change is narrow. It stays inside the internal/redact rules plus one test fixture and comes with table tests, so only the engineer's plan is needed.

## Routing
- triage:engineer: plan a tightly bounded exemption for AWS's documented placeholder values in the aws-access-key rule (and any rule that matches the placeholder secret), keep everything else fail-closed, define the table tests, and say whether redaction (separate from the scan) changes.
- worker engineer: code change in internal/redact and the servecmd test fixture, with unit tests.

## Open issues
- This loosens a security control. Review should confirm that only the exact documented placeholder values, or a strictly anchored placeholder suffix, are exempt, and that an exempt token on a line cannot hide a real key elsewhere on the same line.
- The plan, tests and fixtures should build the placeholder values at runtime, for example by joining their parts, so that no agent log contains a literal scanner match. If a log does contain one, it gets withheld again.
- The gitleaks integration in internal/redact may flag these values on its own. The plan should say whether its result needs handling too.
