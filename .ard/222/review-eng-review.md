---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The three credentialed URI literals are now built with `url.URL` at run time, exactly as plan.md and the issue describe. The assertions are unchanged and the tests pass. Approved.

## Checks
- Lint: `gofmt -l` on the three changed packages is clean. I did not run `make lint`; `go vet` could not finish offline because dependencies had to be downloaded.
- Tests: `go test ./internal/initcmd ./internal/labels/labelsync ./internal/servecmd` passes.
- Literal grep from plan.md finds no credentialed URI literal left under `internal` in `*_test.go` files.
- Diff coverage: not applicable; the change only rewrites test fixtures.
- trufflehog scan: not run; the tool is not available here.
