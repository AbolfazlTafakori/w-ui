---
description: "The GitHub settings to switch on for main and for release tags, so nothing reaches a release that CI and a review have not seen."
---

# Protecting main and releases

These are GitHub settings, made once in the repository's **Settings**. They are not on yet; switch them on once every CI job below has passed on `main` at least once.

## A ruleset for `main`

**Settings → Rules → Rulesets → New branch ruleset**, target `main`:

- [ ] **Restrict deletions** and **Block force pushes**.
- [ ] **Require a pull request before merging** — one approval when there is a second maintainer; dismiss approvals when new commits are pushed.
- [ ] **Require status checks to pass**, with **Require branches to be up to date**. The checks, by the name each CI job shows:
  - *Frontend builds and matches the committed bundle*
  - *Go vet, static analysis and tests*
  - *Installer and menu behave*
  - *Clean install on …* — every entry of the matrix
  - *Upgrade from every earlier release (PostgreSQL)*
  - *Update from … by installer*, *by panel*, *by menu* — every entry
  - *Panel and node across releases …* — every entry
- [ ] **Require linear history**.
- [ ] Bypass: none. The owner merges through a pull request like anyone else; a hotfix too.

## The same checks before `dev-latest`

In `.github/workflows/ci.yml`, the `dev-release` job lists what it waits for under `needs`. Add `upgrade`, `upgrade-postgres` and `node-compat` once they have run green, so the dev channel only carries builds that passed the upgrade tests too.

## A ruleset for release tags

**New tag ruleset**, target `v*`:

- [ ] **Restrict creations** to the maintainers, **Restrict updates** and **Restrict deletions** — a published tag never moves.

The release workflow additionally refuses a tag whose commit CI did not pass on `main` (see the [release checklist](./release-checklist)).

## The signing key

- [ ] Move `WUI_SIGNING_KEY` from a repository secret into an **environment** called `release`, with **Required reviewers** set and **Deployment branches and tags** limited to `v*` tags; set `environment: release` on the release job. A workflow run on any other ref then cannot read the key.
- [ ] `dev-release` signs with the same key: give it the same environment, or a key of its own for development builds.
