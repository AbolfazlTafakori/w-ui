# API

Everything the panel does, it does through its own HTTP API — there is no private path the interface uses and callers cannot.

## Authentication

Either sign in for a session token:

```bash
curl -X POST 'https://panel:2053/PATH/api/auth/login' \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"…"}'
```

or use an **API token** — the one the installer printed, or one from Nodes → tokens or `wui token issue --name NAME` — and send it as:

```
Authorization: Bearer wui_…
```

## The documentation is in the panel

**API** in the sidebar lists every endpoint, grouped, with an example body and a `curl` command carrying the address you reached the panel on. The page is built from the same table the routes are registered from, so it can only describe endpoints that exist, and a new one is documented by being added.

`GET /api/docs` returns the same as JSON.

## A few you will reach for

| | |
|--|--|
| `GET /api/overview/full` | telemetry, panel state and inventory in one call |
| `GET /api/clients?search=&status=&group=&page=&perPage=` | customers |
| `POST /api/clients` | create one; `telegramId`, `quotaBytes`, `expiresAt`, `deviceLimit`, `interfaceIds` |
| `POST /api/clients/{id}/reset` | reset traffic |
| `GET /api/clients/{id}/configs` · `GET /api/devices/{id}/profiles` | every config, per device and host |
| `GET /api/interfaces` · `POST` · `PATCH /{id}` · `DELETE /{id}` | tunnels |
| `GET /api/outbounds` · `POST /api/outbounds/{id}/check` | hops and probes |
| `GET /api/routing` · `POST /api/routing/rules/order` | rules |
| `GET /api/template?section=` · `PUT` | the whole configuration |
| `POST /api/backups` · `GET /api/backups/{name}` | backups |
| `GET /api/system` · `GET /api/system/history` | the host |

Errors come back as `{"error": "…", "field": "…"}` with a status that means what it says; a validation error names the field.
