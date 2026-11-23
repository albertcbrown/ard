---
role: engineer
status: done
attempt: 1
---
Replaces the earlier plan with one that evaluates reviewer when: rules at PR time, using the owner's amendments: directory-rule normalisation, a correct merge-base diff range, rename handling, a pure selection function, and a config check warning. Size L, risk medium because it decides which reviews gate a PR.

## Open issues
- The plan touches `internal/roles/config.yaml` only if it mentions when:; if that file is protected or needs a human edit, a maintainer should do it.
- Needs a look that the committer's declared-path check is acceptable with the stricter rename listing.
