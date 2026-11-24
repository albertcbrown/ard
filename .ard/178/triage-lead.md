---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Phase 4 adds user documentation under docs/, a static-site generator config, Go-side drift checks that compare the CLI commands and config keys named in the docs against the code, and a README link. The engineer can do all of that without touching protected paths. The Pages workflow, any CI workflow steps for the site build and link check, and enabling Pages in settings must be done by a human under .github/. The issue already says so.

## Routing
- triage:engineer: pick the generator, lay out the pages, and plan how the command and config-key checks run from `make test`. Also split the work between the agent PR and the follow-up a human makes under .github/.
- worker engineer: write the docs and site config, add the drift tests, and update the README and the proposal's Rollout section.

## Open issues
- Agents cannot write under .github/, so a maintainer must add the Pages deploy workflow and any CI steps for the site build and link check, then enable Pages. The plan should say exactly what that follow-up contains.
- No product-manager, architect or documentation-planner triage role exists. The engineer has to make the page-structure and generator choices alone, so a maintainer may want to review that plan before the worker starts.
- The scope is large. If one PR gets unwieldy, a maintainer could split it, for example into docs content first and then the site and checks.
- Getting started must be followed by hand on a scratch repository. An agent cannot do that, so a human has to before the issue counts as done.
