---
description: "How W-UI is put together: the layers from the HTTP API down to the kernel, which package owns what, and the rules the tests hold every change to."
---

# Architecture

W-UI is one Go binary. It keeps everything in one database, and a loop makes the kernel match it. This page is the map: where each thing lives, and the rules that keep it there. The rules are checked by `internal/archtest` on every `go test ./...`, so a change that breaks one fails in CI, not in review.

## The layers

Each layer uses only the ones below it.

```
 Entry points      api (HTTP)   tgbot   cmd/wui (CLI and the start)   web (Vue)
 ─────────────────────────────────────────────────────────────────────────────────
 Reporting         oversight: the security audit, the daily report, reminders
 ─────────────────────────────────────────────────────────────────────────────────
 Domain services   service: customers, tunnels, subscriptions, groups, hosts,
                   operators, nodes, settings
                   egress: outbounds, providers, balancers, routing rules
                   nodes: probing and syncing the other servers
 ─────────────────────────────────────────────────────────────────────────────────
 Applying it       reconciler: the loop that makes the kernel match the database
                   backend + one driver per protocol, sharing agentkit
                   enforce (nftables), shaper (tc), routing (fwmark, hops)
 ─────────────────────────────────────────────────────────────────────────────────
 Data              database (+ model, numbered migrations), scope, ipam, backup
 ─────────────────────────────────────────────────────────────────────────────────
 Small pieces      fault, listfield, tunnelport, keyfile, wgkey, totp, certfile,
                   each protocol's conf package, devguide
```

The **root agent** (`l2tpagent`, the `wui-l2tp` service) runs apart from the panel, as root, because pppd, strongSwan and ocserv need root. The panel never does. They talk through files in the data directory: the panel writes what each tunnel should be, the agent writes back a report, every second. `agentkit` is the panel's side of that.

## What each layer may do

- **An entry point reads, checks who is asking, and answers.** A handler decodes the request, calls a service, and writes the result. A rule that lives in a handler is one the bot, the CLI and the node sync do not keep. `api` never queries the database; only `api.go` names it, to build the services.
- **A domain service owns its rules.** A reseller's term starting at the first sign-in is `service.Admins`'s; switching every tunnel off is `service.DataPlane`'s. One rule, one place.
- **A service's failure has a kind.** `fault.ErrInvalid`, `fault.ErrNotFound` and `fault.FieldError` (which names the form field) are what the API turns into 400, 404 and the message under the right input.
- **A reseller's queries are narrowed for them.** `scope` adds the owner to every query on customers, devices and groups. Raw SQL on those tables is refused unless the call site vouches for it (`scope.Vouch`), after showing it narrows by owner itself.
- **The database only grows.** Tables gain columns and indexes every start from the models; nothing an earlier release made is removed (`additive_test.go`, against a database written by every release since 1.0). Anything more is a numbered migration in `database/migrate.go`, run once and recorded in `schema_migrations`.
- **The JSON is a decision.** The models are what the API answers with, so every model's JSON is pinned in `model/testdata/json-shape.golden`, the routes and who may reach each in `api/testdata/routes.golden`, and whole responses in `cmd/wui/testdata/contract`. A change to any of them fails until the golden file is rewritten with `-update` and the diff reviewed.

## The rules the tests enforce

| Rule | Why |
|--|--|
| No package has an exported variable holding a function | A hook set from elsewhere decides what a package does, and whoever sets it last wins. A dependency is given to the instance that needs it. |
| Only `cmd/wui` writes another package's variable | A plain setting (where files go) is set once, at start, before anything reads it. |
| No tunnel driver imports another | What drivers share is in `backend/agentkit`, so changing one protocol cannot break another. |
| `api` does not query the database | See above: handlers ask the services. |
| `egress`, `fault`, `listfield`, `tunnelport` and `backend` do not import `service` | The services are built on them, not the other way. |
| `service` and `egress` do not import `oversight` | What reports on the panel reads it; it is never called back. |
| No database error is discarded | `_ = db.Find(&x).Error` builds an answer out of nothing when the database is busy. |

## Wiring

`cmd/wui/main.go`'s `run` is the start, in order, with each step a function in `start.go`. Every long-lived object is made once there and handed to what uses it: one `service.Clients`, one `service.Interfaces`, one `egress.Tuning` (what the engine page steers), one set of balancers. Tests build their own, and anything not given (`SetLive`, `SetRemote`) falls back to what a CLI would have.

## Watching it run

`GET /api/health` is `200` or `503` for an uptime checker; `GET /api/metrics` is Prometheus text for someone signed in: each tunnel, the loop's last run, failed usage writes, nodes, backups.

## Where to put a new thing

- **A new rule about customers, tunnels or operators**: a method on the service that owns the table, with a test in that package.
- **A new endpoint**: one entry in `api/routes.go`'s table (it documents itself), a handler that calls the service, and `go test ./internal/api -run TestTheRouteTable -update`.
- **A new protocol**: a driver in `internal/backend/<name>driver` implementing `backend.Backend`, registered in `cmd/wui`, using `agentkit` if the root agent runs it.
- **A new column**: on the model, with a JSON tag only if the API should show it, then `go test ./internal/database/model -update` and a look at the diff.
- **Changing old data**: a new numbered migration at the end of the list, written so that running it twice changes nothing.

Coverage of the packages that guard sign-in, reseller isolation, backups and billing has a floor (`scripts/coverage-floor.sh`); CI fails below it.
