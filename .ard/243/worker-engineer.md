---
role: engineer
status: needs-human
attempt: 1
---
The Dockerfile needs the vendor's real SHA-256 checksums for the pinned claude release binaries (amd64 and arm64). I have no network access here beyond the package manager, and the plan says to stop rather than invent values. No code was changed.

## Open issues
- A human should supply the two release checksums for the pinned claude version, or run this where the release checksums can be fetched.
- Everything else in the plan (Dockerfile, test script, Makefile targets, pin-parity test, proposal text) is ready to implement once the checksums are known.
