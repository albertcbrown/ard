---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The kube executor this issue was waiting on has now landed on main (part 6B, PR 408). It provides kube.New, Sweep, the End states including evicted, ErrInputsTooLarge and the RoleSource field, so the serve wiring can go ahead. This goes to the triage engineer for planning and to the engineer worker for the change.

## Routing
- triage:engineer: plan the serve wiring from design revision 3. That covers executor selection, the k8s flags with env fallbacks, the PAT refusal, config drift, RoleSource in the job builders, the new close-out causes, the orphan sweep, token redaction, the config check line and the docs updates. Also check the plan against the kube package as it landed.
- worker engineer: Go change across cli, servecmd, reaper, closeout and the job builders, plus tests using the fake kube client and the docs edits.

## Open issues
- The comments hold two implementation plans. The later one (design revision 3, no listener) replaces the first, so planning should use revision 3 only.
- The issue says size S-M, but the revised plan estimates the lower end of L. A maintainer may want to split it.
- This repository has no product-manager, architect or documentation-planner triage role. The new serve flags and the documented executor and Reaper behaviour changes fall to the triage engineer's plan.
- An operator previously parked this issue until the kube executor merged. That has now happened, but a maintainer should confirm the needs-human hold has been cleared on purpose.
