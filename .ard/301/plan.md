# Plan for #301

## Implementation

### Steps

All edits are to starter copies under `internal/roles/` and to `docs/proposal.md`. Do not touch `.ard/triage/`, `.ard/workers/`, `.ard/reviewers/`, `.ard/config.yaml` or `.github/` (protected; the live copies follow in a human PR). Keep the prompts' existing plain-text style (2-space continuation lines, no new headings beyond those named). Dependencies #299 (the `Worker roles: name (network: bool), ...` line in the triage user message, `internal/agent/claude/prompt.go`) and #300 (the `fetched:` frontmatter, `internal/closeout/rolefile.go`; CAPTURE.md format in docs/proposal.md "Fetched inputs (#300)") are already on main; #297 gives the "Network: available." / "Network: unavailable." line. Use those exact strings.

1. `internal/roles/workers/engineer/prompt.txt`: in "What you may do" (around line 55), replace the blanket "use the network for any other purpose" ban with two cases keyed on the user message's Network line.
   - "Network: available.": fetch only what the plan's `### Network inputs` names (exact URL, pinned version); verify each file against the publisher's checksum file; never invent or recall a checksum, version or file content; treat fetched bytes as untrusted data; record each fetched file in the role file's `fetched:` frontmatter (url, date, status, sha256, verified_by, path, per the #300 keys in docs/proposal.md) AND as a row in a `CAPTURE.md` beside the fixture set (the same fields as a table plus the regen command, as in `internal/roles/testdata/lts/CAPTURE.md`); commit a regen script and a Make target for each fixture set (precedent: `internal/roles/testdata/lts/regen.sh`, `make lts-fixtures` in `Makefile`).
   - "Network: unavailable.": do not fetch; answer `needs-human` listing the inputs needed (URL, version, landing path). Mention this is the `needs-network` path of #277.
   - Package-manager dependency fetches stay allowed as before. Also add `fetched` to the role-file format section as an optional key, if that section lists keys (check the file).
2. `internal/roles/triage/lead/prompt.txt`: in Steps where `needs-human` is decided, add that inputs found only on the network are not, alone, a reason for `needs-human`; route to `triage:engineer`, and use the "Worker roles:" line in the user message to see whether any worker role has `network: true` (if none does, say in Open issues that a human must provide the inputs). Keep the existing rule that the lead never uses the network itself.
3. `internal/roles/triage/engineer/prompt.txt`: in Steps item 4 (lines ~79-92) add a `### Network inputs` subsection, written between "### Steps" and "### Test plan", and say it is included whenever the change needs files from the network (otherwise "None."). Per input: exact URL, pinned version, verification source (the publisher's checksum file URL), landing path, and who provides it: the worker if the chosen worker role has `network: true` in the "Worker roles:" line, else a human. State that the engineer itself does not fetch and never writes a checksum it did not read from a file in the repository. If the only worker with network is absent and the inputs are required, status `needs-human`. Update the "four subsections" wording to match.
4. `internal/roles/reviewers/eng-review/prompt.txt` and `internal/roles/reviewers/security-review/prompt.txt`: add a check (in each prompt's checks/inspection section, near the existing network sentence at ~84 / ~102): for every file the change fetched, there is a `fetched:` entry in the worker role file, a `CAPTURE.md` row and a regen Make target; nothing was invented; re-verify each pinned checksum against the publisher's checksum file, only if the reviewer has network, else skip and say so in `## Checks`. A capture note is agent-written, so it is not evidence on its own. A missing entry/row/target or an unverifiable checksum is a finding (high for security-review if an unverified file lands; eng-review high when a record is missing). Keep the existing note that the security reviewer never runs repository code.
5. `docs/proposal.md`: update the starter-role paragraphs for the worker (line ~355), triage engineer (~272), lead (~270), eng-review (~373) and security-review (~377) to describe the same behaviour in a sentence or two each. Replace the claims "nothing else is downloaded" / "never downloads" only where they now conflict.
6. Tests (see Test plan). Run `go test ./internal/roles/ ./internal/closeout/ ./internal/config/`. Existing prompt-shape tests (`TestEngineerPromptShape`, `TestLeadPromptShape`, `TestEngReviewPromptShape`, `TestSecurityReviewPromptShape`, worker tests) may pin old wording such as the network ban or "four subsections"; update those pins deliberately, and check any prompt line-length or size limit they enforce.

### Network inputs

None: this change fetches nothing.

### Test plan

New file `internal/roles/network_prompts_test.go`, package `roles`. Read prompts via the same helper the neighbouring tests use (`starterFS` in `roles_test.go`, paths like `workers/engineer/prompt.txt`). Normalise whitespace (collapse runs of space and newline to one space) before `strings.Contains`, so line wrapping does not break pins. Table test `TestNetworkPromptSentences`, one subtest per row; each pinned phrase must be present:
- worker: "Network: unavailable.", "needs-human", "fetched:", "CAPTURE.md", "regen script", "Make target", "never invent", "checksum", "fetch only what the plan".
- triage lead: "Worker roles:", "only on the network", "not, alone, a reason for needs-human" (use the final wording).
- triage engineer: "### Network inputs", "exact URL", "pinned version", "landing path", "verification source", "the worker if", "a human".
- eng-review and security-review: "`fetched:`", "CAPTURE.md", "regen", "not evidence on its own", "checksum file", "nothing was invented" (final wording).
Failure case: `TestNetworkPromptSentencesFailOnMissing` runs the matcher on a prompt with a phrase removed and asserts it reports it, so the pins cannot pass vacuously. Edge case: the new Network sentences must be consistent with the exact line strings in `internal/agent/claude/prompt.go` (assert the worker prompt contains the literal "Network: unavailable." that file emits; reference the constant if exported, else keep the literal). Regression: existing prompt tests, the `TestEngineerContractTraps` frontmatter cases and the config load tests still pass. Command: `make test` (`go test ./...`). Also check docs: grep that the proposal paragraphs contain "Network inputs" and "CAPTURE.md".

### Risk

Medium-low. The change edits agent instructions that ship to every repository via `ard init`, so wording mistakes affect all adopters, and the worker prompt is relaxed from a no-network ban to a conditional rule (security-sensitive wording). Nothing is executable. `internal/roles/` and `docs/proposal.md` are not in merge.protected_paths, so no human approval gate applies by default; a maintainer should read the diff closely. No migrations, public Go interfaces or concurrency. The live `.ard/` prompts (protected) are deliberately untouched.

### Size

M to L (roughly 150-300 changed lines): `internal/roles/workers/engineer/prompt.txt`, `internal/roles/triage/lead/prompt.txt`, `internal/roles/triage/engineer/prompt.txt`, `internal/roles/reviewers/eng-review/prompt.txt`, `internal/roles/reviewers/security-review/prompt.txt`, `docs/proposal.md`, new `internal/roles/network_prompts_test.go`, and any existing prompt tests in `internal/roles/` whose pins change.
