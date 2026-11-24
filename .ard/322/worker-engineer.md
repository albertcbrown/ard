---
role: engineer
status: done
attempt: 1
---
Makes review-fix aware of why a round runs and adds a merger path that requests a conflict round instead of going straight to needs-human. The path is off in production: servecmd leaves the merger's Rounds dependency unset, so a conflicting PR still gets needs-human until part B.

## Changes
- reviewfix: capPath takes a round reason and records it; conflict caps use their own comment wording (review wording unchanged). landing treats a conflict round as landed when the head is a merge commit (two or more parents) that has the round's head as an ancestor, skipping the no-code check; otherwise it caps with reason conflict. Review rounds are untouched. The Git interface gains IsAncestor and Parents.
- gitx: new IsAncestor and Parents reads.
- merger: optional Rounds dependency and `(t *tick) conflict(...)` replacing both needs-human conflict calls (gate g and updateBranch). Nil Rounds, an unparsable worker branch or an unconfigured worker role fall back to needs-human. A round already requested at this head defers, a capped one is left to a human, dry run only logs.
- servecmd/merger.go: comment only, Rounds deliberately unset.
- Docs: docs/proposal.md and the merger and reviewfix package docs.

## Testing
Ran `go vet ./...` and `go test ./...`; all pass, and gofmt is clean. New tests: merger/conflict_test.go (request, nil Rounds, unknown worker, pending and capped, dry run, unknown mergeability, update-branch conflict, shared cap with the real reviewfix loop, cap wording); reviewfix tests for merge head landing, plain commit and merge-without-head not landing, unmoved head, and review no-code still capping; a gitx test for IsAncestor and Parents. Existing tests pass unchanged.

## Open issues
- The cap counter is shared, so conflict and review rounds both count toward max_review_rounds; the reason is recorded separately per round event.
- A conflict cap comment lists all requested reviewer roles, as the merger passes them; this is wording only.
