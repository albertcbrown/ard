---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The fix is to make a worker change to any .gitattributes file go to a human, either in the committer's code-path check or as a built-in merge hold, and to name that rule in docs/proposal.md. The change is confined to Go code and the proposal document, so the engineer plans it and the engineer worker implements it.

## Routing
- triage:engineer: decide where the hold lives (internal/committer/codepath.go or the merger's hold logic), write the steps, and plan tests plus the docs/proposal.md wording that names the rule.
- worker engineer: Go change with unit tests and the proposal doc update.

## Open issues
- Agent reviewers already get a raw text diff that ignores attributes (internal/gitx/review.go, internal/reviewer/doc.go). The remaining gap is the collapsed diff that humans see on GitHub, so the plan should cover that and need not rebuild the reviewer diff.
- One option in the issue is to add .gitattributes to merge.protected_paths in .ard/config.yaml. Agents may not edit that file, so if maintainers want that option instead of a code-level hold, a person has to make the config change.
