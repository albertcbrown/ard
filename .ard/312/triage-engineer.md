---
role: engineer
status: done
attempt: 1
---
Re-plans the docs-only edit of docs/proposal.md for the owner's revision 3 of the Kubernetes executor design (pre init container, isolated agent, result pulled from a post container, no API on the controller). It replaces the earlier plan for the tarball-API design. Size S to M, risk low.

## Open issues
- The issue carries a hold: the owner said they will approve this revised plan before the worker starts.
- Gaps 5 (redacting the preshared key by value) and 11 (worktree registration after the swap) are not settled, so the plan records them as unchecked Open questions.
- The Delivery text saying the agent image is never published conflicts with the owner's public-image answer. The plan leaves it for the later image issue and notes the conflict.
- The worker must match on text, not line numbers, because the cited lines have drifted.
