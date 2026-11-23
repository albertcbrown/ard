---
role: engineer
status: done
attempt: 1
---
Makes the finding decoder's errors content-free. It replaces quoted values and raw JSON library messages with a line number and a fixed reason class, and adds a table test using secret-shaped inputs. Size S, risk low.

## Open issues
- The JSON library's own error text can echo scanner characters and key names, so the plan classifies those errors as well as the quoted fingerprint and file values.
- This overlaps with the pre-filing secret guard work in issue 215 around scan-failed logging. Rebase if it lands first.
