---
description: "What W-UI promises not to break, and how anything that has to go is retired: the API, settings, subscription links, the CLI, files and the node protocol."
---

# Deprecation policy

Thousands of servers update unattended, with scripts, client apps and other panels depending on what they do today. What follows is what they may rely on.

## What is covered

| Contract | Pinned by |
|---|---|
| The REST API: paths, methods, fields, status codes, who may call what | `internal/api/testdata/routes.golden`, `cmd/wui/testdata/contract/` |
| Subscription links and what they serve | the contract tests and the upgrade tests (every link from v1.0.0 on) |
| Settings, by stored key and value | the upgrade fixtures, every key with a non-default value |
| The database | migrations only add (`TestMigrationsOnlyAdd`) |
| Backup archives | every earlier release's backup restores (`TestEveryEarlierBackupRestores`) |
| The `wui` and `w-ui` commands, installer flags | the installer tests; the docs |
| Files under `/etc/wui` and `/var/lib/wui`, systemd units, nftables names and marks | the upgrade CI job's before-and-after comparison |
| The panel-to-node protocol | the node compatibility CI job |

## The rules

1. **Additive by default.** A new field, endpoint, setting, flag or file is added beside the old ones. Nothing an earlier release wrote or answered is renamed.
2. **Nothing is removed in a patch or minor release** without the steps below. A removal in a major release (3.0) still follows them.
3. **Deprecate first, in writing.** The CHANGELOG gets a *Deprecated* entry saying what replaces it; the docs mark it; the panel logs a warning each time it is used (an API answer also carries a `Deprecation: true` header).
4. **Keep it working for at least two minor releases and 90 days**, whichever is longer, after the release that deprecated it.
5. **Remove it with a *Removed* entry** and what to do instead, under *Updating* if an operator must act.

## Never

- **A subscription link handed to a customer stops working** while the customer exists, unless the operator rotates it. The token, the path on the subscription port, and what a client app reads (the configuration, `Subscription-Userinfo`, `Profile-Title`) keep their shape.
- **A setting is renamed.** A new key is added, the old one is read as a fallback, and both are carried by backups.
- **A migration drops or renames a column, a table or an index** — except an index replaced on purpose, listed with why in `internal/database/additive_test.go`.
- **A panel stops managing a node one minor release behind it**, or a node stops answering a panel one minor release behind.
- **A published release tag is moved or deleted.**

## Settings keys and the CLI in detail

- A settings key is read under its old name as long as any supported release could have written it.
- A CLI subcommand or flag that goes keeps working as an alias that prints what replaces it.
- An installer flag is accepted, and ignored with a note if it no longer does anything, rather than failing the install.
