# Plan for #142

## Implementation

The rule: a worker may not commit a file named `.gitattributes` at any depth. It joins `.gitmodules` as a refused name in `committer.CheckCodePath`, the one copy of the path rule that both the worker diff guard and `CommitCode` call. A rejection already holds the job as `ard:needs-human` with nothing pushed, so a human sees every attributes change. The merger's `merge.protected_paths` is not used, because the design says that list has no floor in code (#3) and `.ard/config.yaml` is human-owned. Agent reviewers already get an attribute-blind raw diff (`internal/gitx/review.go`), so that part needs no change.

### Steps
1. `internal/committer/codepath.go`: next to the `.gitmodules` check, add `if strings.EqualFold(segs[len(segs)-1], ".gitattributes") { return reason("attributes file (can hide changes from review)") }`. Update the doc comment's "Refused:" list. Case-insensitive, matching `.gitmodules`. `.git/info/attributes` is already refused by the `.git` segment rule.
2. `internal/committer/codepath_test.go`: add `.gitattributes`, `sub/.gitattributes`, `a/.GitAttributes` to `refused`. Add `.gitattributes.md`, `docs/gitattributes.txt` to `allowed`.
3. `internal/worker/unit_test.go` (line ~87): add `rc(".gitattributes")` to `rejects`.
4. `internal/roles/workers/engineer/prompt.txt` (line ~59): add `.gitattributes` to the rejected-paths bullet beside `.gitmodules`. Check `internal/roles/worker_engineer_test.go` (the want list near line 74) still passes; add `".gitattributes"` to that list.
5. `docs/proposal.md`: in the diff-guard paragraph (step 5, "the path rule in `committer.CheckCodePath` (...)") add `.gitattributes` to the list. Add the same to the "Role files are human-owned" or nearby security bullet, naming the rule: any `.gitattributes` change by a worker holds the job for a human, because `linguist-generated` or `-diff` collapses files in the GitHub PR diff.

### Test plan
- `go test ./internal/committer/ ./internal/worker/ ./internal/roles/`, then `go test ./...` (Makefile `test` target).
- TestCheckCodePath (`internal/committer/codepath_test.go`): refused `.gitattributes`, `sub/.gitattributes`, `a/.GitAttributes`; allowed `.gitattributes.md`, `docs/gitattributes.txt`, `dir/.gitignore` (existing).
- Worker guard test (`internal/worker/unit_test.go`): a change to `.gitattributes` is rejected.
- Prompt test: the prompt mentions `.gitattributes`.
- Edge: nested and mixed-case names are refused; similarly named files are not.

### Risk
Low. A narrow added refusal in one function. Public behaviour change: workers can no longer edit `.gitattributes` (legitimate edits, such as line-ending config, now need a human). No migration, concurrency or secrets involved. `internal/roles/workers/engineer/prompt.txt` is the embedded starter prompt, not `.ard/workers/`, so it is not in the default protected paths, but check `merge.protected_paths` in `.ard/config.yaml` before the worker starts.

### Size
S. Files: `internal/committer/codepath.go`, `internal/committer/codepath_test.go`, `internal/worker/unit_test.go`, `internal/roles/workers/engineer/prompt.txt`, `internal/roles/worker_engineer_test.go`, `docs/proposal.md`.
