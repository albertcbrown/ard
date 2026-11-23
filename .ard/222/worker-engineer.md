---
role: engineer
status: done
attempt: 1
---
Builds the three credentialed-URL test fixtures at run time with `url.URL` and `url.UserPassword`, so no URI-shaped secret literal remains in the source.

## Changes
- `internal/initcmd/initcmd_test.go`, `internal/labels/labelsync/cmd_test.go`, `internal/servecmd/serve_test.go`: replace the literal origin URL with a `url.URL{...}.String()` expression and add the `net/url` import. The resulting URLs are identical to the old literals.

## Testing
Ran `make lint test`; all pass. The grep for credentialed URI literals in these files finds nothing. A trufflehog scan was not run (tool not used locally).

## Open issues
None.
