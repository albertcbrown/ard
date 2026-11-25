---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The change adds `idna>=3.15` with an explanatory comment to docs/site/requirements.txt. This fixes the advisory in the issue and matches the existing certifi entry. Approved.

## Checks
- Lint: none applies; only a requirements text file changed.
- Diff coverage: skipped; no code lines added.
- I did not re-run the dependency resolve; the diff is a single added pin.

## Previous findings
- The previous report had no findings.
