# Design: node-only API tokens

Status: proposed for v2.6.2. Not implemented.

## The problem

Since v2.6.1 an API token passes `requireManager` again, as it did before
v2.0.0, because a managing panel reaches its node through one. That makes
every token as strong as the owner on the machine: 103 routes a reseller and
a panel administrator are refused (interfaces, routing, outbounds, VPN
providers, the engine, starting and stopping tunnels, asking for an update;
see `internal/api/testdata/routes.golden`). A managing panel needs six of
them. A token leaked from a managing panel -- its database, a backup of it, a
log -- should open those six and nothing else.

## What a managing panel calls on its node

From `internal/nodes` (prober, syncer, update request), exactly:

| Route | Why |
|---|---|
| `GET /api/system` | the probe: is the node up, which version |
| `POST /api/node/sync` | the desired state of one tunnel and its customers |
| `POST /api/node/usage` | collect and reset the node's usage counters |
| `POST /api/node/sessions` | who is connected, for the connection limit across servers |
| `POST /api/node/hold` | hold a customer over their limit on this node |
| `POST /api/system/update` | ask the node to update itself |

The progress of a node's update (`GET /api/system/update/progress`) and what
release is available (`GET /api/system/update`) are added, so a later panel
can show a node's update the way it shows its own.

## Design

### Data

`api_tokens` gets one column, added (the migration stays additive):

```go
// Scope is what the token opens: "full" -- everything a token reaches
// today -- or "node" -- the routes a managing panel uses on its node.
Scope string `gorm:"size:16;not null;default:full" json:"scope"`
```

Every existing token reads as `full`: nothing that works today stops working
on update, an automation token included. The default for a new token is
decided by the caller, not the column.

### Authentication

`Nodes.VerifyToken` returns the token row (or its scope) instead of a bool.
`requireAuth` puts the scope in the context beside `ctxMachine`. One check,
in `requireAuth`, before any gate:

```go
if machine && scope == "node" && !nodeRoutes[r.Method+" "+r.Pattern] {
	writeError(w, http.StatusForbidden, "this token is for a managing panel and opens only what one uses")
	return
}
```

`nodeRoutes` is the table above, declared once in `routes.go` beside the
route table, so a route a managing panel starts to call is added in the same
change that makes it call it -- and the route golden test shows the change.

### API

- `POST /api/tokens` takes `{"name": "...", "scope": "node" | "full"}`.
  Absent is `full`, so a script written against v2.6.1 issues the token it
  always did.
- `GET /api/tokens` returns each token's `scope`.
- `PATCH /api/tokens/{id}` may narrow a token from `full` to `node`, never
  widen one: widening is issuing a new token, which is an operator's decision
  made in the open.
- `wui token issue --name NAME [--scope node|full]`; absent is `full`.

### Panel

- **Settings → Security → API Token → Issue**: a choice, *For a panel that
  manages this one (node only)* -- preselected -- or *Full access, for
  automation*. Each token in the list shows its scope.
- **Nodes → Step 2** (the docs and the page that explains adding a node) asks
  for a node-only token.
- The installer's `installer` token stays `full`: it is the operator's own
  automation handle, printed once, kept in `install-result.env`.

### Existing nodes

A node token issued before v2.6.2 is `full`. The node's token list shows it
with a note, *issued before node-only tokens existed -- narrow it*, and a
button that narrows it in place (`PATCH ... {"scope":"node"}`). Nothing is
narrowed automatically: a token used by both a panel and a script would
otherwise break the script.

### Docs

`docs/panel/nodes.md` (step 2), `docs/reference/api.md` (what a token can do,
by scope), `docs/operations/security.md`, the CHANGELOG, and the errors page
for the new message -- English and Persian.

## Tests

- **Route golden**: a sixth column, `node-token`, read through the real gates
  like the others. Exactly the routes in the table above say `yes`.
- **Auth unit tests**: a node token reaches each of those routes and gets 403
  on, at least, `PUT /api/interfaces/{id}`, `POST /api/routing/rules`,
  `PUT /api/engine`, `POST /api/tunnels/stop`; a full token is unchanged; a
  `PATCH` cannot widen.
- **Upgrade test**: the fixtures' tokens (and a token in a new fixture) read
  as `full` after migrating; the migrations-are-additive test covers the
  column.
- **Node compatibility job**: the managing panel uses a node-only token, in
  both directions (a v2.6.1 node does not know scopes and ignores the field --
  the token it issues is full, and still works).
- **Mutation checks**: dropping the scope check, adding a route to
  `nodeRoutes`, or letting `PATCH` widen a token each fail a test.

## Rollout

v2.6.2: the column, the check, the API and CLI field, the page, the docs.
Issuing a node token defaults to node-only from that release. No existing
token changes.
