---
role: engineer
status: done
attempt: 1
---
Adds a new stdlib-only `internal/tarball` package with streaming tar.gz pack and safe unpack through `os.Root`, typed errors, configurable limits, table and fuzz tests, and a layout doc line. The plan follows design revision 3, so the swap helper is out of scope. Size L (about 850 lines), risk medium-high because unpack is the security boundary for agent output.

## Open issues
- The issue body still lists the swap helper, but the later plan moves it to #317B; the plan excludes it.
- The package adds shared exported types (Root, Limits, Stats, Error, ErrUnsafe, ErrLimit) that later parts will use, so a maintainer may want to confirm the API shape.
- The security reviewer should look closely at the rejection cases and the fuzz test. The security-review reviewer is not requested here because I could not confirm its trigger rule.
