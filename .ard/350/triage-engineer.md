---
role: engineer
status: done
attempt: 1
---
Adds `ard setup --create-gh-app` to internal/setupcmd. It uses one built-in permission definition shared with the setup check, a one-shot 127.0.0.1 callback server, the manifest code exchange, and a 0600 key file. The plan has ten steps, tests for each acceptance criterion, and the getting-started docs change. Size L, risk medium because it opens a local listener and handles a private key.

## Open issues
- The setup skeleton this depends on is already on the main branch. The permission check in internal/setupcmd/cmd.go is still a placeholder until #302 lands, so the shared definition is wired in there for now.
- The optional 1Password write could be split into a follow-up if the change grows too large.
- A maintainer should review the callback server and key handling closely.
