---
role: engineer
status: done
attempt: 1
---
Neutralises markup and mentions in scanner titles centrally in the scanner pipeline, and gives the starter scanners one inline-code rule for paths and identifiers in bodies, so repository content cannot put live markup or a notification into a public issue.

## Changes
- `internal/scanner/marker.go`: new `SafeTitle` (control characters collapsed, backtick to apostrophe, `<` `>` `[` `]` swapped for lookalikes, word-initial `@` to fullwidth `＠`; idempotent) used by `IssueTitle` (before the length cut, with the scanner name as fallback for an empty title), checklist lines and the details summary. New `InlineCode` for file lists, and `neutralizeBody`, which defuses mentions and raw HTML tags in a finding's body outside fences and closed code spans.
- `docscan.go`: `code()` and a new `clean()` apply the same rule to titles and bodies.
- `errcodes.jq`, `cve/merge.jq`: same rule for titles, paths and package names (`@` to `＠`, `<`/`>` removed).
- `docs/proposal.md`: new "Scanner text is untrusted" paragraph in the Scanners section.
- Tests: `TestSafeTitle`, `TestIssueTitleHostile`, `TestDetailBlockHostile` (marker_test.go); hostile-path tests for docs and error-codes; a `hostile_name` cve fixture; two goldens updated for the new title rule (backtick and `<>` in a docs title, `＠` in a scoped npm title).

## Testing
`go test ./internal/scanner ./internal/roles` passes. `go build ./...` and the rest of the suite could not run here: module downloads are blocked, so packages outside these two were not tested.

## Open issues
- `.ard/scanners/cve/merge.jq` is protected and not edited; it now differs from the starter copy until a maintainer applies the same change.
- Scoped npm titles now read `＠scope/name` (fullwidth at-sign) by design.
- The docs scanner's `code()` is a second copy of the rule, since it cannot import the pipeline package.
