---
role: engineer
status: done
attempt: 1
---
Makes local-tree writing the default for `ard init` and keeps the PR lane behind a new `--pr` flag. It also removes label sync from init and adds a permission check taken from a helper factored out of `ard setup`. The plan has six steps with tests. Size L, risk medium because it changes a public command's default and writes into the working tree.

## Open issues
- No shared permission check exists yet (#302 is not on main), so the plan factors the setup placeholder into an exported function that both commands call. #302 can replace its body later.
- Local mode should not need a GitHub App, so a missing repo or credentials only warns there. `--pr` fails.
- The dirty-tree check may need a small new helper in `internal/gitx`.
