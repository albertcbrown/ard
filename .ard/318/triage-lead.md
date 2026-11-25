---
role: lead
status: blocked
attempt: 1
---
This issue wires the Kubernetes executor into the serve command, but the executor it wires in has not landed yet, so it is blocked. Only the kube REST client and its fake (part 6A) are on main. The kube executor itself is still missing: there is no kube.New, no Sweep, no End states such as evicted, no inputs-too-large error and no RoleSource field.

## Routing
None until the dependency lands. Once it does, route to triage:engineer and triage:documentation-planner if it exists. This change adds serve flags and changes documented executor and Reaper behaviour. Today only triage:engineer exists. The worker will be engineer.

## Open issues
- Waits for issue 317, the second half of part 6 (the kube executor package under internal/executor/kube). The serve wiring builds on its constructor, its sweep method, its end states and its errors.
- The comments include two implementation plans, and the later one (design revision 3) replaces the first. The triage engineer should work from revision 3 only.
- A maintainer should check that the size is still right. The issue says S-M, but the revised plan estimates L at the lower end.
