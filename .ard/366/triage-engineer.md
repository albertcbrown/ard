---
role: engineer
status: done
attempt: 1
---
Makes the update-branch cover count a merge only when its tree equals what git's own clean merge of its two parents produces, for every merge in the chain, and fails closed when that cannot be computed. The existing patch-id comparison stays for the shared-file rule. Size M, risk medium because it tightens a security-relevant gate.

## Open issues
- Minimum git is already 2.47 (`gitx.MinVersion`), above the 2.38 that `merge-tree --write-tree` needs, so no version bump is planned. A runtime failure still means not covered.
- The raw-diff hash is not adopted: its hunk headers carry line numbers, so it would break the covered case of a clean merge that shifts lines. The mechanical-merge check closes the whitespace gap, because git's own merge cannot introduce whitespace edits.
- A clean merge authored by ard itself is indistinguishable from any other clean merge and stays covered. Only non-mechanical merges (conflict resolutions) lose cover.
- `merge-tree --write-tree` writes tree objects into the controller's clone.
