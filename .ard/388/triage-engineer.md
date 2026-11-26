---
role: engineer
status: done
attempt: 1
---
Adds a narrow skip for upper-case snake-case values (at least one underscore, no lower case, at most 64 characters) to the generic-assignment placeholder check in internal/redact/rules.go, with table tests for the new negatives and for values that must still be reported. Size S, risk medium because the log scan, committer scan and diff guard all share the rule.

## Open issues
- The plan describes patterns in words and avoids literal secret-looking assignments, because the scanner would flag them.
- The proposal has no paragraph on the generic-assignment rule, so no doc note is planned.
