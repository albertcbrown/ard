---
role: engineer
status: done
attempt: 1
---
Lets a live `ard init --pr` use a token from the environment or the gh fallback by reusing serve's resolution, removes the dry-run-only check, rewords the PR body's follow-up section per auth kind, and updates the docs. Size M, risk low to medium.

## Open issues
- The plan edits docs/proposal.md; confirm no other docs say init needs an App.
- With a token, init PRs are authored by the token's user, so the PR author may be the approver; the runbook already notes this for serve.
