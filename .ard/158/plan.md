# Plan for #158

## Implementation

### Steps
1. `internal/redact/secrets.go`: add a package-level (new) `nonSecretEnv = map[string]bool{"ARD_AGENT_AUTH": true}` with a comment: it holds a mode name (`auto`, `oauth`, `apikey`), not a credential. Do not import `internal/modelauth` (keeps redact dependency-free); the comment names `modelauth.EnvMode` instead.
2. Same file, `FromEnviron`: after the `environRE` match, `continue` when `nonSecretEnv[strings.ToUpper(k)]`. This is an exact-name exclusion, so `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY` and every other AUTH/TOKEN/etc. name keep being redacted. Update the `FromEnviron` doc comment to mention it.
3. `internal/redact/secrets_test.go`: add the test below. `AddEnv` and `Add` are unchanged (explicit adds still warn on short values).

### Test plan
Command: `go test ./internal/redact/...` (then `go test ./...`).
- `TestFromEnvironSkipsAgentAuthMode` (new, `internal/redact/secrets_test.go`): swap `slog.Default()` for a buffer handler as in `TestShortValueSkippedAndLoggedByName`; call `FromEnviron([]string{"ARD_AGENT_AUTH=auto", "CLAUDE_CODE_OAUTH_TOKEN=" + randStr(50, 40), "ANTHROPIC_API_KEY=" + randStr(51, 40)})`. Expect: log buffer has no `ARD_AGENT_AUTH` and no "too short"; both credentials are redacted in `s.String(...)`; the text `auto` is left unchanged.
- Edge: lowercase `ard_agent_auth=oauth` is also skipped (covered by `ToUpper`); another short AUTH-like variable, e.g. `MY_AUTH=abc`, still warns (assert in the same test to prove the exclusion is narrow).

### Risk
Low. Security-sensitive (redaction) but the exclusion is one exact name that holds no secret. No protected paths (`merge.protected_paths`) are touched, no public interface changes, no concurrency.

### Size
S (about 25 changed lines). Files: `internal/redact/secrets.go`, `internal/redact/secrets_test.go`.
