---
role: engineer
status: blocked
attempt: 1
---
The work is blocked, so the plan was left untouched. A maintainer's security review says the cover check for update-branch merges must first learn not to treat a conflict-round merge as one, and that change is tracked in #366, which is not on the default branch yet.

## Open issues
- Once #366 lands, re-run triage. The existing Implementation section needs a rewrite first, because the security review found it unsafe as written.
- The tree rule in step 2 contradicts itself. The merge result must differ from the merge parent only in the PR's declared paths, and each of those must pass the path guard. Everything else, including `.github/`, `.ard/`, `.gitmodules` and `.gitattributes`, must match main byte for byte. The preferred design has no `git merge` in the committer: reset to the merge parent, apply the snapshot, check staged changes against it, and commit with explicit parents of the branch tip and the merge parent. More than 256 declared files should end as needs-human.
- The worker must not run git in the agent's worktree after the agent step. It should record the conflicted paths from its own pre-agent merge and read the files through the snapshot or a root-confined open.
- Any merge that runs must be hardened: an empty-tree attribute source, rerere, renormalize, submodule recursion, autocrlf and gpgsign disabled, `--no-verify --no-edit -s ort`, and no `-X` from config. Conflict-hunk diffs should use `--no-textconv --no-ext-diff`.
- The merge parent must be strict. Fetch main from the remote URL into a private ref, recheck it on every attempt, take it only from the worker's own fetch, and never retry in merge mode. A moved branch returns a conflict error. Add the second parent to `CommitOp` so dry-run logs both parents.
- The review's should-fix items still need steps and tests: compute the diff guard from the fetched main tip, cap and escape `conflict.md`, make the marker check advisory and limited to conflicted paths, and end cleanly as needs-human when GitHub rejects a workflow-file merge. They also include committing a merge whose tree equals the branch tip, requiring the second parent to be on main's first-parent history in the review-fix loop, and the listed negative tests.
- The part of the original plan that touches the starter config is still a human task, because those files are protected.
