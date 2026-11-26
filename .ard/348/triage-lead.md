---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This extends `ard setup` in two ways. It sets the repository variable for the bot login, taken from the App identity, through a new effect-layer call. It also checks the repository's bot-commits workflow against a copy embedded in ard and adds a flag that prints that copy. Both dependencies (the first `ard setup` command in `internal/setupcmd` and the GitHub connectivity and token work) are already in this checkout, so the work can start now. The engineer triage role is the only planner, so it plans the whole change.

## Routing
- triage:engineer: implementation steps for the new effect-layer method, the variable check and apply (including asking before replacing a different existing value), the embedded workflow comparison and the print flag, plus tests and doc updates.
- worker engineer: Go change in the setup command and effect layer, with unit tests.

## Open issues
- The change only reads the existing workflow file under `.github/workflows/` and embeds a copy of it in ard. It must not edit anything under `.github/`. If the embedded copy needs to differ from the committed file, a human commits the change to the workflow.
- The CLI gains a new flag and setup gains a new apply step. No product-manager, designer or documentation-planner role exists, so the engineer plan has to cover the user-facing messages and the doc updates.
