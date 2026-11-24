---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The cve scanner reported a critical advisory against certifi in the documentation site's Python dependencies, with a fixed release available. This is a narrow dependency fix confined to docs/site/requirements.txt, so only the engineer needs to plan it.

## Routing
- triage:engineer: decide how to raise certifi to the fixed release and how to verify the docs build still works.
- worker engineer: dependency pin change in the docs site requirements.

## Open issues
- certifi is not listed directly in docs/site/requirements.txt (only mkdocs and mkdocs-material are pinned), so it arrives transitively. The fix likely means adding an explicit certifi pin at or above the fixed version, or upgrading a direct dependency that pulls it in.
