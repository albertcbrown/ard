---
role: lead
status: blocked
attempt: 1
---
What's left on this issue is the starter architecture scanner role and its rollout. It is still blocked on the LLM scanner engine tracked in #204. The filing rules from #204 are now on main, but the engine itself is not, so the issue stays blocked until that lands.

## Routing
None.

## Open issues
- Filing rules from #204 are now in the tree: `LLMScanner`, `CheckLLMFingerprints`, the near-duplicate skip and the autoclose check under `internal/config` and `internal/scanner`.
- The engine half of #204 is still missing. There is no `AgentEngine`, no `agentjob.ScannerCommand` and no `$ARD_OUT` output directory, and `internal/scanner/loop.go:120` still skips LLM scanners. The role's prompt writes its findings to `$ARD_OUT`, so shipping it now would give every adopter a role that never runs.
- Once the engine merges, route this to triage:engineer with the engineer worker. The work is the starter role under `internal/roles/scanners/architecture`, its starter `config.yaml` entry and the `ard init` updates. A maintainer should review the shipped prompt because it goes to every adopter.
- The rollout step that adds the scanner to this repository's own `.ard/scanners/` and `.ard/config.yaml` touches protected paths. A human has to do it, not an agent.
