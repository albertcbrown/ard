---
role: engineer
status: done
attempt: 1
---
Adds the stdlib-only leaf package `internal/tarball`: deterministic tar.gz pack of explicit roots and a safe, limit-capped unpack through `os.Root`, per design revision 3 (the swap helper is out of scope; it moves to #317B).

## Changes
- `internal/tarball/{doc,errors}.go`: threat model, `ErrUnsafe`/`ErrLimit`, `*Error` (classifiable with `errors.Is`).
- `internal/tarball/pack.go`: `Root`, `PackStats`, `Pack`. Symlinks stored as links, `.git` skipped case-insensitively, special files skipped and counted, size-change detection, a missing `Files` entry is left out.
- `internal/tarball/unpack.go`: `Limits`, `DefaultLimits`, `Stats`, `Unpack`. Checks path, depth, length, `.git`, duplicates, case-fold collisions, paths under or over symlinks (either order), entry types and modes. Compressed, decompressed and ratio caps via counting readers. Symlinks are created last.
- `internal/tarball/{unpack,pack}_test.go`: reject table, gzip bomb (streamed), modes, dest checks, bad input, round trip, Files root, skips, symlink-to-secret, root names, `FuzzUnpack`.
- `docs/layout.md`: one line for the package.

## Testing
`go vet ./internal/tarball`, `go test ./internal/tarball` pass; `FuzzUnpack` ran 20s with no failures; seed corpus runs under plain `go test`.

## Open issues
- A NUL in an entry name cannot reach the unpacker through `archive/tar` (it cuts ustar names at NUL and the writer ignores PAX path records), so that check is covered by a direct `cleanName` unit test rather than an archive case.
- Ratio cap of 100 can reject very compressible but legitimate output; the limit is in `DefaultLimits`.
- Full `make test` was not run; only the new package and docscheck.
