---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/closeout/facts.go"
    line: 36
    severity: high
    blocking: true
    message: |
      The FactsOf round-trip of TokensUnreported has no test, and plan.md's test plan names one.
      This is the only path that carries the flag from the stored record to the cost-table row, so a regression here would silently drop the mark. Add a test that stores a record with the flag and checks FactsOf returns it.
  - file: "internal/costs/rows.go"
    line: 47
    severity: high
    blocking: true
    message: |
      No test covers the costs loop setting the row mark (plan.md: "a closed job with the flag yields a row with the mark; one without does not").
      Add a costs test with one closed job flagged and one not, and check the rendered rows.
  - file: "internal/reaper/finish.go"
    line: 255
    severity: high
    blocking: true
    message: |
      The issue's done-when says the mark appears once, also on a resumed close-out, and plan.md lists MarkOnceOnResume (first Run errors, second succeeds, one marked comment). It was not written; the worker relied on reading the code.
      Add that test, so the flag is shown to survive ResumeJob rather than assumed.
---
The change implements the token ledger as planned and its own tests pass. It needs changes because three tests that plan.md names are missing: the FactsOf round-trip, the costs row mark, and the resumed close-out showing the mark once.

## Checks
- `make lint`: runs `go vet ./...`. It fails on `internal/roles/testdata/docs/skips/src/_x.go:3:8: expected ')'`. That is a deliberately broken testdata file the change did not touch, so I ignored it. Nothing was reported on the changed files.
- `go test` for config, state, closeout, costblock, costs and reaper: all pass.
- Diff coverage: no profile run. By reading, the added lines in `facts.go` and `costs/rows.go` are not exercised by any new test (see findings). The rest is covered.
