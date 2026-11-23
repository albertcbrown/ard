# Plan for #222

## Implementation

### Steps
1. `internal/initcmd/initcmd_test.go` (subtest "origin token is not echoed", ~line 429): add `"net/url"` to the imports (alphabetical, after `log/slog`). Replace the literal `"https://x:secret@gitlab.com/a/b.git"` with `(&url.URL{Scheme: "https", User: url.UserPassword("x", "secret"), Host: "gitlab.com", Path: "/a/b.git"}).String()`. Keep the `strings.Contains(err.Error(), "secret")` assertion.
2. `internal/labels/labelsync/cmd_test.go` (subtest "remote with a token is not echoed", ~line 220): add `"net/url"` to the imports (after `errors`). Replace the same literal with the same `url.URL` expression.
3. `internal/servecmd/serve_test.go` (`TestResolveRepoFromOrigin`, ~line 353): add `"net/url"` to the imports (after `log/slog`). Replace `"https://user:secrettoken@example.com/o/r"` with `(&url.URL{Scheme: "https", User: url.UserPassword("user", "secrettoken"), Host: "example.com", Path: "/o/r"}).String()`. Keep the `"secrettoken"` assertion.
4. Optionally build each URL into a local variable first (for example `remote := ...`) so the `set-url` line stays readable. Do not add `trufflehog:ignore` comments or exclude paths.

### Test plan
- Existing subtests "origin token is not echoed" (initcmd), "remote with a token is not echoed" (labelsync) and `TestResolveRepoFromOrigin` (servecmd) must still pass. Each sets a credentialed https origin and expects `resolveRepo` to fail without the password in the error text.
- Sanity check: `String()` yields `https://x:secret@gitlab.com/a/b.git`, so the inputs are unchanged. The same is true for the servecmd URL.
- Run `go test ./internal/initcmd ./internal/labels/labelsync ./internal/servecmd`, then `make lint test`.
- Check with `grep -rn '://[^" ]*:[^" @]*@' internal --include=*_test.go` that none of the three files still contains a credentialed URI literal. A trufflehog scan is optional and only if the tool is available locally.

### Risk
Low. The change touches test files only. It touches no protected paths, public interfaces, migrations, production code or concurrency.

### Size
S (about 10 to 15 changed lines). Files: `internal/initcmd/initcmd_test.go`, `internal/labels/labelsync/cmd_test.go`, `internal/servecmd/serve_test.go`.
