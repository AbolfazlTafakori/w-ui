# Upgrade fixtures

Each `vX.Y.Z/` directory is what that release made, by itself, from an empty
data directory: the panel built from the release's own tag, given the same set
of tunnels and customers through its own API. The tests in `cmd/wui` start
today's panel on each one and check that nothing an operator or a customer
depends on has changed.

| File | What it is |
|---|---|
| `wui.db` | The release's SQLite database, copied with `VACUUM INTO` while it ran |
| `manifest.json` | What that panel said about its interfaces and customers, every stored setting, and each customer's subscription token and link |
| `sub/<customer>.conf` | The configuration each customer's link served |
| `backup.tar.gz` | A backup that panel took |

## What is in each

Three tunnels (`fx-wg` WireGuard, `fx-awg` AmneziaWG, `fx-ovpn` OpenVPN) and
six customers: limited and unlimited, a plan that starts on first connection,
an OpenVPN login, one switched off, and one on two tunnels. Dates are fixed
(2031-01-01), so they read the same in every fixture. The subscription service
runs on a port of its own from v1.1.0, the release that brought one; v1.0.0
serves links on the panel's port, as it did.

Keys, tokens and passwords are the ones each release generated. They are test
data, made for these files and used nowhere else:

- administrator `fixture` / `fixture-password-1`
- OpenVPN login `fxovpn` / `fixture-ovpn-pass`
- the JWT secret in each database's settings

## Making them again

```bash
scripts/upgrade/make-fixtures.sh              # every release
scripts/upgrade/make-fixtures.sh v2.7.0       # add a new one
```

Each release is built from its tag in a throwaway git worktree, so the working
tree is not touched. It runs on Linux and in Git Bash on Windows; the panel
answers its API without root, which is all a fixture needs.

Add the newest release after each one ships. An existing fixture is never
regenerated to make a test pass: it is what that release really wrote, and a
test that fails on it has found a server an update would break.

## PostgreSQL

The PostgreSQL fixtures are made where a server is, by CI, and not committed:

```bash
WUI_FIXTURE_PG_DSN=postgres://… OUT=/tmp/pgfx scripts/upgrade/make-fixtures.sh
WUI_TEST_PG_DSN=postgres://… WUI_UPGRADE_PG_FIXTURES=/tmp/pgfx go test ./cmd/wui -run TestUpgradeOnPostgres
```
