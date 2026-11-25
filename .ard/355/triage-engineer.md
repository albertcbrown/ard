---
role: engineer
status: done
attempt: 1
---
Adds a per-match exemption to the aws-access-key scan rule for 20-character ids ending in the documented placeholder suffix, enforced in the scanner loop. It also changes the servecmd fixture to a runtime-built real-shaped id and adds table tests. Size S to M, risk low to medium because a security control is loosened.

## Open issues
- The documented placeholder secret needs no code change, since the generic rule already skips values containing the word example. A test will pin that.
- Redaction is unchanged. The optional gitleaks scanner is left alone and may still flag the placeholder on its own.
- Test values must be built from parts at runtime so no source or log contains a literal match.
