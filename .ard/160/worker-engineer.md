---
role: engineer
status: done
attempt: 1
---
Tightens the starter eng-review prompt so the reviewer runs the repository's own lint entry point (make lint, or what the README, CI or worker config names) instead of a tool it picks, and falls back only when none exists, saying so under Checks.

## Changes
- internal/roles/reviewers/eng-review/prompt.txt: rewrote the Lint bullet in Checks (lookup order, no substitution, fallback note, behaviour when the Trust rule blocks the entry point); first example now shows `make lint`.
- internal/roles/eng_review_test.go: TestEngReviewPromptShape requires the new phrases.

## Testing
Ran `go test ./internal/roles/`, `make lint` and `make test`; all pass.

## Open issues
- The live .ard/reviewers/eng-review/prompt.txt is protected and was not changed; a maintainer may copy the edit by hand.
