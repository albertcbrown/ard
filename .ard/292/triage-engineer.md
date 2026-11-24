---
role: engineer
status: done
attempt: 1
---
Adds three optional repo context templates to the embedded starter, their protected paths, the init path check and a create-if-absent action in `ard init`, with matching proposal edits. The plan has nine steps with tests for each completion criterion. Size M, risk medium-low.

## Open issues
- The design in the parent issue was not among the planner's inputs, so the template headings and the precedence and conflict wording must be taken from it by the worker.
- This repository's own `.ard/config.yaml` is not changed; a maintainer must add the three paths to its `merge.protected_paths` by hand if wanted.
- `config.SourcePaths()` is left alone; the init snapshot appends the three paths so the plan can see them on the base branch.
