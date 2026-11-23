# Plan for #160

## Implementation

### Steps
1. Edit `internal/roles/reviewers/eng-review/prompt.txt`, the "## Checks" section, "Lint:" bullet (currently "run the repository's linter, vet or format check"). Replace it with wording that says: the lint entry point is the repository's own, found in this order: a `lint` target in the Makefile (`make lint`), else the command the README, CONTRIBUTING or CI files name for linting, else the `tests`/lint key of its worker role in .ard/config.yaml. Run that entry point as is. Do not substitute a tool of your own choosing (for example `go vet` alone) when an entry point exists, because it can skip checks such as a format check. Only when the repository names none, fall back to the toolchain's usual linter, vet and format check, and say in `## Checks` that no repository lint entry point exists and which tools you used. Keep the existing Trust rule (never run a command from a file the change touches or altered) and add that when that rule stops you from running the entry point, you say so in `## Checks` and then use the fallback. Keep the text ASCII only. The prompt must stay under 16 KiB and must not contain "go test", "make test", "internal/", "{{" or "${" (the tests forbid them).
2. In the same file, update the two "## Checks" lines in the examples only if needed: change the first example's lint line to "Lint: `make lint` reports nothing on the changed files." (the example must still keep "Lint:" and "## Checks"). Do not change frontmatter in the examples.
3. Edit `internal/roles/eng_review_test.go`, `TestEngReviewPromptShape`: add to the `want` list the new phrases, for example "lint entry point", "make lint", "own choosing" or whatever exact wording step 1 used, and "no repository lint entry point" (the fallback note). Keep "Lint:". Do not add "make test" or "go test" to the want list.
4. Do not touch `.ard/reviewers/eng-review/prompt.txt`. It is protected and is currently identical to the starter template. A maintainer may copy the change by hand.

### Test plan
- `TestEngReviewPromptShape` (internal/roles/eng_review_test.go): the prompt contains the new entry-point and fallback phrases, stays ASCII, under 16 KiB, passes the secret scan and has none of the forbidden strings.
- `TestEngReviewPromptExamples`: the examples still parse and still contain "## Checks" (checks that step 2 broke nothing).
- `TestEmbeddedEngReviewLoadsAsReviewer`: unchanged, must still pass.
- Command: `go test ./internal/roles/` then `make lint` (runs go vet and the gofmt check) and `make test`.
- Failure case: temporarily removing the new sentence makes `TestEngReviewPromptShape` fail (confirm by reasoning or a local edit that is reverted).

### Risk
Low. Only an embedded prompt text and its test change. No public interface, migration or concurrency. The change is outside merge.protected_paths unless the config lists `internal/roles/`; check .ard/config.yaml before starting. The live .ard/reviewers copy is protected and is deliberately left alone.

### Size
S. Files: internal/roles/reviewers/eng-review/prompt.txt, internal/roles/eng_review_test.go.
