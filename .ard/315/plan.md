# Plan for #315

## Implementation

Follows design revision 3 (the newest plan comment): no controller job API, key table or TLS. Two pieces: scoped read-token mint/revoke in `internal/github`, and a new leaf package `internal/jobpod` holding the controller-to-pod contract. `internal/jobapi` is not created.

### Steps

1. **New `internal/github/scoped.go`**, plus a small edit to `internal/github/auth.go`.
   - In `auth.go`, `installation()` (line 99) records the installation id (the configured `a.InstallationID` or the one found by `discover`) in a new field, guarded by `a.mu`. Add `func (a *AppAuth) installationID(ctx) (int64, error)` that calls `installation()` when the id is unknown.
   - `type ScopedToken struct{ Value string; ExpiresAt time.Time }`. Both `String()` and `LogValue() slog.Value` return `"[token]"`.
   - `var ErrCannotScope`, `var ErrNotInInstallation` (new, in `scoped.go`; follow the style of `errors.go`).
   - `func (a *AppAuth) MintReadToken(ctx, repos []Repo) (ScopedToken, error)`. Check first that every repo has the installation's owner, else `ErrNotInInstallation` with no request made. Then `POST {base}/app/installations/{id}/access_tokens`. Sign with `appsTransport` (auth.go:180) and send through `newAPIClient` (http.go:217, no redirects). Body is `{"repositories":[names],"permissions":{"contents":"read"}}`. 201 parses `token` and `expires_at`. 422 maps to `ErrNotInInstallation`. Anything else goes through `statusError` (errors.go:97).
   - `func (a *AppAuth) RevokeToken(ctx, tok string) error`: `DELETE {base}/installation/token`, `Authorization: Bearer <tok>`, through `newAPIClient`. 204 and 401 return nil; other codes go through `statusError`.
   - `func ScopedMinter(a Auth) (*AppAuth, error)`: `*AppAuth` is returned as is. `*TokenAuth` and anything else give `ErrCannotScope`, with the message "a personal access token cannot be scoped down; the k8s executor needs GitHub App auth".
2. **New `internal/jobpod/contract.go`** (constants and pure helpers). It imports only `executor` and `gitref`. Define the values listed in the plan comment: container names, UIDs/GID, volume mount paths, `executor.Paths`-shaped agent view, Secret item names, result route (`ResultPort`, `ResultPortName`, `ResultPath`), archive roots, `DoneName`, exit codes, and label keys. Also `PodName(id)` (`"ard-job-"+ToLower`, with `MaxJobID=55`), `RepoLabel(repo)` (16 hex of the sha256 of the lowercased repo) and `LabelValue(s)` (truncate to 63 with a short hash, always a valid label value). Add a package doc comment saying that #316 and #317 import this package and never each other.
3. **New `internal/jobpod/manifest.go`**: `Manifest`, `Source`, `MaxManifestBytes = 768<<10`, `ErrTooLarge`, `Encode` (the cap is measured on the encoded JSON) and `Decode`. `Decode` checks `V`, 40-hex SHAs with `gitref.IsSHA`, the repo shape, and that `Inputs` and `Scrub` paths are clean and relative (no `..`, no absolute paths). `Inputs` is `[]executor.File`.
4. **New `internal/jobpod/secret.go`**:
   - `NewKey() (string, error)`: 32 bytes from `crypto/rand`, raw base64url.
   - `SecretData(manifest []byte, token, key, modelName, modelValue string) (map[string][]byte, error)`: refuses a `modelName` other than `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN`.
   - `KeyMatches(got, want string) bool`: `subtle.ConstantTimeCompare` over the sha256 of both.
5. **`docs/layout.md`**: add an `internal/jobpod/` line next to `internal/tarball/` (line 26): controller-pod contract (paths, Secret items, manifest, result route); imports only executor and gitref. Run `make docs-check` since a docs checker exists (`internal/docscheck`). The proposal.md sidecar-auth sentence also gains the k8s scoped-token clause (check the exact line first; the line number in the plan comment may have moved).

Order: 1, 2, 3, 4, 5. Each step builds on its own.

### Test plan

Command: `go test ./internal/github/... ./internal/jobpod/...`, then `go test ./...` and `make docs-check`.

- `internal/github/scoped_test.go`, using httptest as the API:
  - `TestMintReadToken` (table). The request body has the repo names and `contents: read`, and carries the JWT. 201 is parsed. 422 gives `ErrNotInInstallation`. A repo with another owner is refused with zero requests. A 302 is not followed. A 500 is an error.
  - `TestRevokeToken`: 204 and 401 give nil, 500 gives an error, and the Bearer header carries the token.
  - `TestScopedMinterRefusesPAT`: `NewTokenAuth` gives `ErrCannotScope`.
  - `TestScopedTokenNotLogged`: slog JSON and `%v`/`%s` output lack the value.
- `internal/jobpod/manifest_test.go`: round trip. `ErrTooLarge` at the cap plus one encoded byte, with a boundary case that passes. Refused: bad SHA, an input path `../x`, an absolute scrub path, an unknown `V`.
- `internal/jobpod/contract_test.go`: a 55-char id gives a `PodName` of at most 63 chars, all lowercase. `LabelValue` of a 100-char role is a valid label. `RepoLabel` is valid. No two Secret items share a name.
- `internal/jobpod/secret_test.go`: `TestKeyMatches` (equal, different, empty), `NewKey` has the right length and differs per call, and `SecretData` refuses an unknown model name.

### Risk

Medium. It mints GitHub credentials, so a wrong body could give more than contents:read; the body test pins the scope. The token must never be logged (`LogValue` test). No protected paths from `merge.protected_paths` are touched. New exported API in `internal/github` and a new package, but there are no callers yet. There are no migrations and no concurrency beyond the mutex on the installation id.

### Size

L, lower end: about 330 lines of code and 350 of tests, under the 3,000-line default block limit. Files: `internal/github/auth.go` (small edit), `internal/github/scoped.go`, `internal/github/scoped_test.go`, `internal/jobpod/{contract,manifest,secret}.go` with their `_test.go` files, `docs/layout.md`, and the proposal.md line.
