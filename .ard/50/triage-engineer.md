---
role: engineer
status: needs-human
attempt: 1
---
The full scanner is still size XL (well over 1,000 changed lines), so no implementation plan was written for this issue. A comment records that the work has already been split into two smaller issues, #212 (the Go helper) and #213 (the scan driver, Dockerfile and starter wiring), and this issue stays open only as a tracker.

## Open issues
- Plan and implement the work under #212 and #213, not here. Ask for engineer triage on those issues if it has not been done.
- The CLAUDE.md Layout line change in the second part touches a protected path, so that PR needs human approval.
- Enabling the scanner on ard itself (`.ard/scanners/`, `.ard/config.yaml`, `deploy/`) is a human change, because agents may not edit those paths.
- A maintainer should close this issue when #213 merges.
