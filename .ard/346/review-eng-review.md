---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/setupcmd/cmd.go"
    line: 20
    severity: high
    blocking: true
    message: |
      plan.md's test plan names TestCheckExitsTwoOnDrift, which was not written. Command is only tested for the bad-config path.
      The exit-code-2 mapping on --check drift, the "manual steps needed" error and the Check/DryRun interaction (`dry := e.DryRun && !o.Check`) have no test. Command takes no injectable seams, so split out the part after effects.Open (or let tests pass a fake client) and assert a *cli.ExitError with Code 2 on drift.
  - file: "internal/cli/cmd_setup.go"
    line: 11
    severity: medium
    blocking: false
    message: |
      The plan's cli tests were not written: that `--check` reaches `SetupOpts{Check:true}` through a handler, that `ard labels sync` still reaches LabelsSync, and that help lists `setup`. Only the nil-handler stub and the command path were added. Add a table case with a recording handler.
  - file: "internal/setupcmd/cmd.go"
    line: 84
    severity: low
    blocking: false
    message: |
      The plan said LocalVsMain also checks the worktree; this only calls ChangedSince(root, root, origin/main, ".ard/"). Confirm this covers uncommitted edits, or document the limit.
---
The core Run/Print logic is correct and well tested, but the plan's TestCheckExitsTwoOnDrift for Command (the new exit code 2) is missing, so this needs changes.

## Checks
- `make lint`: go vet passes on the changed packages. The run stopped on a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, which this change does not touch.
- `go test -cover` on setupcmd, cli and labels/...: all pass. setupcmd coverage is 66%. Command's apply and exit-code paths are not executed by any test.
