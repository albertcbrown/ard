---
role: engineer
status: done
attempt: 1
---
Adds one central title sanitiser and a shared inline-code rule to the scanner pipeline, applies the rule in the docs, error-codes and cve starter scanners, and states it in the proposal's scanner section, with hostile-input table tests for each scanner. The plan has eight steps. Size L, risk medium because titles of existing issues change on their next edit.

## Open issues
- The repository's own cve scanner copy under .ard/scanners/ is a protected path and the plan does not edit it. It will differ from the starter copy under internal/roles until a maintainer applies the same change by hand.
- The docs scanner is built standalone and cannot import the pipeline package, so its inline-code helper is a second copy of the same rule.
