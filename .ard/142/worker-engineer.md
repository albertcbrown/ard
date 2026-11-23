---
role: engineer
status: done
attempt: 1
---
Refuses any worker change to a `.gitattributes` file in `committer.CheckCodePath`, so the job is held for a human. This stops `linguist-generated` or `-diff` from collapsing files in the PR diff unseen.

## Changes
- `internal/committer/codepath.go`: refuses a file named `.gitattributes` at any depth, case-insensitively, like `.gitmodules`. The doc comment is updated.
- `internal/committer/codepath_test.go`, `internal/worker/unit_test.go`: refused and allowed cases for the rule. The guard test's "and N more" count goes from 5 to 6.
- `internal/roles/workers/engineer/prompt.txt` and `internal/roles/worker_engineer_test.go`: the prompt lists `.gitattributes` as a rejected path, and the test checks it.
- `docs/proposal.md`: the diff-guard paragraph names the rule and why it exists.

## Testing
Ran `go test ./...`. The first run failed on the "and 5 more" count in the worker guard test, because one more path is now rejected. I fixed the expected count. After that, the committer and worker packages pass, and the other packages passed in the first run. gofmt reports nothing.

## Open issues
- Workers can no longer make legitimate `.gitattributes` edits without a human.
- Adding `.gitattributes` to `merge.protected_paths` as well is a human config change and was not made.
