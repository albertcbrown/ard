---
role: lead
status: needs-human
attempt: 1
---
The issue asks for a new security reviewer role. That means adding files under .ard/reviewers/, which agents may not change, so a person has to take this on or split it first. The issue also says it depends on issue #42, so it could not start yet anyway.

## Routing
None.

## Open issues
- The role's prompt.txt and any config.yaml override (model, a when: rule) live in protected paths (.ard/reviewers/ and .ard/config.yaml). A maintainer has to write those.
- Code support for the deterministic aids (running semgrep or gosec plus a secret scan and passing their output to the reviewer) could be filed as a separate issue for an agent to implement. It would need triage:architect and triage:engineer, because it adds external tool dependencies and changes the reviewer loop.
- The issue depends on issue #42, which has not landed yet as far as this triage can tell. Once a maintainer splits out the code work, that issue should be routed as blocked until #42 lands.
- The acceptance check (catching planted issues in sample PRs) needs sample PRs or fixtures. A maintainer should decide where they live.
