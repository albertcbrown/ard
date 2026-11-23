---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is a small hardening fix in the scanner package. Finding-decode errors should report a line number and a reason class and never echo text supplied by the scanner, so that nothing secret-shaped reaches the controller log. The change stays inside internal/scanner and its tests, so only the engineer needs to plan it.

## Routing
- triage:engineer: list the error sites in the finding decoder, the reason classes to use in their place, and the table test that proves no scanner-supplied value appears in an error.
- worker engineer: change the error messages in the decoder and add the table test.

## Open issues
- This overlaps with the pre-filing secret guard work in issue 215, which touches the scanner pipeline's scan-failed logging. The worker should rebase onto it if it lands first.
