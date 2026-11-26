---
role: engineer
status: done
attempt: 1
---
Adds a `SetRepoVariable` effect (operator token, in memory only) and uses it in `ard setup` to set the bot login variable from the App identity. It asks before replacing a different value. It also checks the bot-commits workflow on `origin/main` against an embedded copy and adds `ard setup --print-workflow`. The plan has eight steps with tests and doc updates. Size M, risk medium because it makes a credentialed write and the variable controls the bot-commit audit.

## Open issues
- The embedded copy is a new file under `internal/setupcmd/workflow/`, kept equal to the committed workflow by a test. A human must commit any change to the workflow under `.github/`.
- The confirmation prompt needs a way to read from the terminal. If `cli.Env` has none, the worker should refuse to overwrite and print the manual command, and a maintainer may want a `--yes` flag.
