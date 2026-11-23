---
role: engineer
status: done
attempt: 1
---
Adds an optional gitleaks scanner to the redact package, combined with the builtin scanner, switched on by a new `redact.gitleaks` config block and wired through the close-out redactor. It fails closed on a missing binary, an error exit or a bad report, and a hit takes the existing flagged path. Size M, risk medium because it runs an external binary on log content and adds a config key.

## Open issues
- Shipping the pinned gitleaks binary in the image needs changes to the Dockerfile or deploy/, which are protected paths. The plan leaves them out, and the real-binary integration test skips unless a binary path is supplied through an environment variable.
- Enabling the pass in this repository needs a change to `.ard/config.yaml`, which a maintainer must make. It stays off by default.
- A maintainer should confirm that #20 is complete.
