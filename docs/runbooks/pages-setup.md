# Publishing the docs site (human steps)

Agents cannot write under `.github/`, so a maintainer does these steps once. The site is MkDocs Material, built from `docs/site/` with `mkdocs.yml`.

## 1. Add `.github/workflows/docs.yml`

- **Triggers:** `push` to `main` and `pull_request`, both with `paths: [docs/site/**, mkdocs.yml, internal/config/**, internal/cli/**, internal/docscheck/**]`.
- **`build` job:** check out, set up Python and Go, `pip install -r docs/site/requirements.txt`, then `make docs-check` (`mkdocs build --strict` plus the Go drift test).
- **`deploy` job** (push to `main` only, `needs: build`): `actions/configure-pages`, build to `site/`, `actions/upload-pages-artifact`, `actions/deploy-pages`. Permissions: `contents: read`, `pages: write`, `id-token: write`. Environment `github-pages`.

## 2. Enable Pages

Settings, Pages, Source: **GitHub Actions**. The site appears at `https://albertcbrown.github.io/ard/`.

## 3. Optional

Add the docs build to the required checks. Because `docs/site/` is not a protected path, docs pull requests merge without a human unless you add it.

## 4. Check by hand

Follow Getting started on a scratch repository to a first triaged issue and note the result on the pull request.
