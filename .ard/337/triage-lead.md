---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The cve scanner reported a medium advisory against pymdown-extensions in the documentation site's Python requirements. The package is a transitive dependency of mkdocs-material, so the fix is to pin or constrain it to the fixed release and confirm the site still builds. This only touches the docs build, so the engineer can plan it alone.

## Routing
- triage:engineer: decide whether to add a direct constraint in the docs requirements or bump mkdocs-material, check that mkdocs-material accepts the fixed major version, and say how to verify the docs build.
- worker engineer: dependency change in the docs requirements file.

## Open issues
- The fixed version is a major-version jump (10.x to 11.0). mkdocs-material 9.5.50 may not allow it, so the plan may also need a mkdocs-material upgrade. No architect role exists to review that.
- I found no mkdocs configuration in the repository that enables the affected b64 extension, so the vulnerable code is probably not reachable. A maintainer may still want the upgrade to clear the finding.
