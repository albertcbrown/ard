---
role: engineer
status: done
attempt: 1
---
The redactor no longer collects ARD_AGENT_AUTH from the environment. It holds a mode name, not a credential, so the "secret value too short" warning is gone. Real credentials stay redacted.

## Changes
- internal/redact/secrets.go: new `nonSecretEnv` exact-name set (ARD_AGENT_AUTH); `FromEnviron` skips those names, case-insensitively.
- internal/redact/secrets_test.go: `TestFromEnvironSkipsAgentAuthMode`.

## Testing
Ran `go test ./...` and `gofmt -l`; all pass. The new test checks that:
- no warning is logged for ARD_AGENT_AUTH, in upper or lower case;
- CLAUDE_CODE_OAUTH_TOKEN and ANTHROPIC_API_KEY are still redacted;
- another short AUTH-named variable (`MY_AUTH`) still warns.

## Open issues
None.
