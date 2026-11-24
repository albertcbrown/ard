---
role: engineer
status: done
attempt: 1
---
Adds tests proving that per-repository paths and the serve lock never collide between distinct repositories and agree for case variants of the same one. No collision was found, so no production code changed.

## Changes
- internal/servecmd/isolation_test.go (unix build tag): a `repoPaths` helper collecting state (live and dry-run), lock, bare clone, work, logs, config and roles-repo paths; tests for distinct pairs (equality and path-prefix checks across all roots), case folding, invalid repos, the live/dry-run state split, and leader locks (two repos both succeed, same repo and case variant fail, succeeds after release).

## Testing
Ran `go test ./internal/servecmd/ -run 'Repo|Lock'`, `gofmt -l` and `go vet` on the package; all clean. The earlier full package run also passed.

## Open issues
None.
