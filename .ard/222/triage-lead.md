---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
A test-only change: three existing tests in separate internal packages should build their credential-shaped URLs at run time instead of holding them as source literals, matching the pattern the redact package's tests already use. No production code, interface or documented behaviour changes, so only the engineer plans it.

## Routing
- triage:engineer: steps for rewriting the three test fixtures and checking that the tests, lint and a secret scan stay clean.
- worker engineer: edit the three test files.

## Open issues
- The issue describes this as a prerequisite for enabling the trufflehog scanner on this repository. Adding that scanner under .ard/scanners/ is a separate human change and is out of scope here.
- The scan acceptance check depends on the trufflehog role's shipped exclude-paths, which the worker may not be able to run locally; the reviewer may need to rely on the test diff alone.
