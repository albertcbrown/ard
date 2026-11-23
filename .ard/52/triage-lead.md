---
role: lead
status: blocked
attempt: 1
---
The work left on this issue is the starter architecture scanner role and its rollout. Both need the LLM scanner engine and its filing rules, which have moved to issue #204, and that work is not on main yet. This issue should wait for #204 to land before it is routed.

## Routing
None.

## Open issues
- Blocked on #204, which now covers the filing rules (fingerprint grammar check, near-duplicate skip, no pending label, autoclose config error) and the agent engine. The working tree has no `LLMScanner`, `CheckLLMFingerprints` or `AgentEngine` yet, and the remaining role's tests depend on them.
- After #204 merges, this issue can go to triage:engineer with the engineer worker for the starter role under `internal/roles/scanners/architecture`, its starter `config.yaml` entry and the `ard init` updates. A maintainer should review the shipped prompt, since it goes to every adopter.
- The rollout step that adds the scanner to this repository's own `.ard/scanners/` and `.ard/config.yaml` touches protected paths, so a human has to do it. An agent cannot.
