# Plan for #314

## Implementation

Scope: design revision 3 (the later plan in the comments). The swap helper is NOT part of this issue (it moves to #317B); the body's mention of it is superseded. The post container packs, the controller unpacks. Package `internal/tarball` is new, a stdlib-only leaf (`archive/tar`, `compress/gzip`, `os.Root`; go.mod is go 1.27 so `Root.Symlink` is available). Root names come from the caller; the package knows no pod layout.

### Steps

1. New `internal/tarball/doc.go`: package comment with the five-line threat model (nothing in an archive is trusted; the unpack side runs as the controller uid; every write goes through `os.Root`). Declares `var ErrUnsafe = errors.New("tarball: unsafe entry")` and `var ErrLimit = errors.New("tarball: limit exceeded")`.
2. New `internal/tarball/errors.go`: `type Error struct{ Entry, Why string }`, `Error() string`, and `Is(target error) bool` so `errors.Is(err, ErrUnsafe|ErrLimit)` works (add an unexported `kind error` field). `Entry` is the cleaned name truncated to 200 bytes; `Why` is fixed text.
3. New `internal/tarball/pack.go`:
   - `type Root struct{ Name, Dir string; Files []string }` (Name: one validated segment, not empty, no `/`, not `.` or `..`, not `.git`; Files non-empty means only those regular files directly under Dir).
   - `type PackStats struct{ Entries int; Bytes int64; Skipped int }`.
   - `func Pack(w io.Writer, roots []Root) (PackStats, error)`: gzip + `tar.Writer`, `FormatPAX`, uid/gid 0, zero ModTime. `filepath.WalkDir` in lexical order, never following links. Dirs, regular files, and symlinks as `TypeSymlink` with `Linkname` verbatim. Skip any path with a `.git` component (case-insensitive). Skip and count sockets, FIFOs and devices. Modes 0755 for dirs and files with any x bit, else 0644. Error if a file's size changes during the read (copy exactly header size; check the byte count). Close the tar and gzip writers and return their errors.
4. New `internal/tarball/unpack.go`:
   - `type Limits struct{ MaxCompressed, MaxBytes, MaxFileBytes int64; MaxEntries, MaxPathLen, MaxDepth, MaxRatio int }`; `DefaultLimits()` returns 512 MiB, 2 GiB, 100 MiB, 200000, 1024, 64, 100.
   - `type Stats struct{ Entries int; Bytes int64 }`.
   - `func Unpack(r io.Reader, dest string, l Limits) (Stats, error)`: check that dest is an existing empty dir, then `os.OpenRoot(dest)` and defer Close. Wrap the input in a counting reader (MaxCompressed) before gzip and the decompressed stream in a counting reader (MaxBytes). The ratio check applies once decompressed > 1 MiB. Per entry, after `tar.Reader.Next` has normalised the name:
     - Reject NUL, a leading `/`, `..`, empty or `.` segments, length > MaxPathLen, depth > MaxDepth, any `.git` segment (`strings.EqualFold`), and an entry count above MaxEntries.
     - Reject exact duplicates and case-fold collisions (maps keyed by the cleaned name and by `strings.ToLower`). A directory entry repeating a name already created as a dir is also a duplicate.
     - Reject a path below an earlier symlink entry, and a symlink whose name is a prefix of an earlier path (either order).
     - Types allowed: Dir, Reg, Symlink only. Everything else (hardlink, char/block, FIFO, sparse, GNU or unknown) is ErrUnsafe.
     - Modes: drop setuid/setgid/sticky. Dirs 0755 via `root.MkdirAll`. Files via `root.OpenFile(O_CREATE|O_EXCL|O_WRONLY)`, 0755 if any x bit else 0644, copied with `io.CopyN(f, tr, MaxFileBytes+1)` and refused over MaxFileBytes (also refuse when `hdr.Size` > MaxFileBytes up front).
     - Create parent dirs of a file with `root.MkdirAll`.
     - Collect symlinks and create them last with `root.Symlink`; targets are not validated (documented: no reader follows them).
     - Return at once on error and leave the partial tree; the doc comment says the caller removes dest. Gzip or tar read errors are returned wrapped (not ErrUnsafe) unless a limit counter caused them (ErrLimit).
5. `docs/layout.md`: add a line `internal/tarball/` (safe streaming tar.gz pack/unpack through os.Root; leaf, stdlib only) in the same format as its neighbours, e.g. after `internal/costblock/`.

### Test plan

Command: `make test` (`go test ./...`); also `go vet ./...`, and for iteration `go test ./internal/tarball -run Fuzz -fuzz FuzzUnpack -fuzztime 30s`. A test helper builds archives with `tar.Writer` plus a raw-header helper (write the 512-byte header by hand with checksum) for hostile names such as NUL.

`internal/tarball/unpack_test.go`, `TestUnpackRejects` (table). Each case asserts `errors.Is(err, ErrUnsafe)` or `ErrLimit` as stated and that a sibling sentinel dir beside `dest` stays empty:
- `../x`, `/abs`, `a/../../x`, NUL in name (Unsafe).
- symlink `a`→`..` then `a/f`; file `a/f` then symlink `a` (Unsafe).
- hardlink, char device, FIFO (Unsafe).
- duplicate `f`; dir `d` then file `d`; `README` and `readme` (Unsafe).
- `.git/config`, `sub/.git`, `x/.GIT` (Unsafe).
- too many entries, file over MaxFileBytes, total over MaxBytes, compressed over MaxCompressed, path too long, too deep (Limit).
- gzip bomb of 1 GiB zeros generated through `io.Pipe`, never held in memory, so the ratio or MaxBytes trips (Limit).
- non-empty `dest`; `dest` missing; `dest` is a file (error).
- Truncated gzip and garbage input: error, no panic.

`TestUnpackModes`: 04755 file → 0755; 0600 → 0644; 0777 dir → 0755.
`TestRoundTrip` (`pack_test.go`): nested dirs, executable, empty dir, relative and dangling symlinks pack then unpack to the same Lstat view (type, mode, Linkname); a `Files` root selects only `stdout`.
`TestPackSkips`: `.git` file and `.git/` dir absent; a FIFO (`syscall.Mkfifo`, skip on windows via build tag or `t.Skip`) is skipped and counted in `Skipped`.
`TestPackSymlinkToSecret`: link to an absolute temp file containing `SECRET-MARKER` is stored as `TypeSymlink` and the decompressed archive bytes lack the marker.
`TestPackRootName`: invalid names (`""`, `a/b`, `..`, `.git`) are errors.
`FuzzUnpack`: seed corpus is the reject-case archives; it asserts no panic and no path created outside `dest` (walk the parent dir). The seed corpus runs under plain `go test`.

### Risk

Medium-high. This is the security boundary for agent output: a missed case writes outside the job dir as the controller uid. `os.Root` refuses escapes on its own, and explicit checks plus the fuzz test add depth. Watch PAX/GNU long names (use `hdr.Name` only after `tar.Reader` normalised it) and case-insensitive filesystems on macOS. No protected paths from merge.protected_paths are touched (only `internal/tarball/` and `docs/layout.md`). No public CLI interface or migration changes; it adds new exported types that #315/#316/#317 will depend on, so the API shape is worth a maintainer glance. No concurrency (Unpack/Pack are single-goroutine; the bomb test uses a pipe writer goroutine).

### Size

L: about 400 lines of code and 450 of tests, roughly 850 changed lines, under the 3,000-line worker limit. Files: `internal/tarball/{doc,errors,pack,unpack}.go`, `internal/tarball/{pack,unpack}_test.go` (new), `docs/layout.md` (edit).
