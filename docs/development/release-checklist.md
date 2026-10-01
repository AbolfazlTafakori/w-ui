---
description: "How a W-UI release is made: main green first, a soak on dev-latest, the tag only after CI passed on that exact commit, and what to check once it is out."
---

# Release checklist

A release reaches every server that presses **Update** in its panel, runs `w-ui update`, or installs fresh. Nothing on this list is optional.

## 1. Before anything is tagged

- [ ] **CHANGELOG**: a `## [X.Y.Z] — YYYY-MM-DD` section with what changed, under *Added*, *Fixed*, *Changed*, *Deprecated*, *Removed* or *Updating* — the release workflow refuses a tag without one, and its text becomes the release notes. Anything an operator must do by hand (an update order, a node to update itself) goes under *Updating*, in plain words.
- [ ] The compare link for `[X.Y.Z]` at the bottom of the CHANGELOG, and `[Unreleased]` moved on.
- [ ] The version in `docs/reference/w-ui.md` and `docs/fa/reference/w-ui.md`.
- [ ] Docs, in English and Persian, for anything an operator sees; `docs/reference/errors.md` for any new message (a test checks every documented message exists in the source).
- [ ] An API, setting, link or CLI change follows the [deprecation policy](./deprecation-policy).

## 2. main is green

- [ ] The commit, and an empty `W-UI X.Y.Z` commit, are on `main` and pushed.
- [ ] **Every CI job is green on that commit**: the frontend, Go (vet, staticcheck, govulncheck, `go test -race`), the installer, the clean installs on every distribution, the upgrade jobs (installer, the panel's button and the menu, from the previous release and from v1.1.0), the PostgreSQL upgrade from every earlier release, and the panel-and-node compatibility job.
- [ ] A test that failed and was "fixed" by regenerating a golden file or a fixture is looked at again: the golden files (`internal/api/testdata/routes.golden`, `cmd/wui/testdata/contract/`) change only when the change is the point.

## 3. A soak on dev-latest

The `dev-latest` pre-release is rebuilt from every green push to `main`. Before the tag:

- [ ] On a test server running the previous release: `w-ui` → **Update to Dev Channel**. The panel comes back, customers stay connected or reconnect within the time the upgrade job reported, links answer.
- [ ] One customer of each kind connects: WireGuard, AmneziaWG, OpenVPN.
- [ ] With nodes: the panel reaches each node, and a customer added on the panel appears on the node.

## 4. The tag

```bash
git tag -a vX.Y.Z -m "W-UI X.Y.Z"
```

```bash
git push origin vX.Y.Z
```

Only once step 2 is green. The release workflow checks it anyway: its first job waits for CI on `main` to finish for exactly the tagged commit — looking every 30 seconds, for up to 30 minutes — and nothing is built unless it finished green. If it finished red, never ran, or was still going when the wait ran out, the release stops; once CI is green, re-run the workflow from the Actions tab. It also refuses to publish without the signing key.

- [ ] The release has eight files: `wui-linux-amd64` and `wui-linux-arm64`, a `.sig` beside each, `SHA256SUMS`, `install.sh`, `w-ui.sh`, `update.sh`.

## 5. After it is out

- [ ] On a server on the previous release, the panel offers the update and installs it.
- [ ] The release is announced, with the *Updating* section if there is one.
- [ ] Its upgrade fixture is added: `scripts/upgrade/make-fixtures.sh vX.Y.Z`, the entries for it removed from `newSinceNewestFixture` in `cmd/wui/upgrade_test.go`, and the upgrade job's starting release moved on.
- [ ] A published tag is never moved or deleted. A mistake ships as X.Y.Z+1.

## Testing the release guard without releasing

The check the release workflow runs first can be run by hand; it only reads:

```bash
GITHUB_TOKEN=<a token that can read Actions> go run ./tools/release ci-passed -repo AbolfazlTafakori/w-ui -sha "$(git rev-parse HEAD)"
```

It waits while CI is still going (`-every`, `-timeout`), then prints the green run or why there is none. Its tests (`go test ./tools/release`) cover a green run, a red one, one still running that then goes green or red, one that outlasts the wait, a re-run, another branch and another commit, and check that `release.yml` waits for it and fails without the signing key.
