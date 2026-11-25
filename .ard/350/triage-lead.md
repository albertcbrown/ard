---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a flag to the setup command that creates the GitHub App through the manifest flow. That means a built-in permission definition shared with the setup checks, a loopback callback server, the code exchange, and storing the private key safely. The setup skeleton this depends on is already on the main branch (internal/setupcmd, from the "ard setup skeleton" commit), so the work can start now and goes to the engineer for planning and implementation.

## Routing
- triage:engineer: implementation steps for the manifest builder, the shared permission definition, the 127.0.0.1 callback server with a state check and timeout, the conversion call, writing the key file with mode 0600, the dry-run mode, the docs update, and the tests (permission parity, key redaction, user-owned and org-owned repos with an httptest fake).
- worker engineer: Go code in internal/setupcmd and related packages, with unit tests and the getting-started docs change.

## Open issues
- The only triage role that exists is engineer. No product, architecture or documentation planner is available, even though this adds a user-visible CLI flag, opens a local HTTP listener and handles secrets. A maintainer may want to review the security-sensitive parts (key handling, callback server) closely.
- The optional 1Password write is opt-in and depends on an external tool. A maintainer could defer it to a follow-up if it makes the change too large.
- The dependency in the comments points to the setup skeleton issue. The repository history suggests that work has merged, but a maintainer should confirm that before the worker starts.
