---
description: "Sell one plan across several servers. What a node is, how to prepare the second server, how to add it, how customers get a config for every server, and what to do when a node goes quiet."
---

# Nodes — more than one server

A **node** is a second server running its own W-UI, added to this panel by address. Once added, you manage everything from here: you create tunnels *on* the node, put customers on them, and the customer's one subscription carries a config for every server. One plan, one allowance, several servers — when one is blocked, the rest keep working.

There is no separate agent. The node is a full W-UI panel; this panel talks to it over the same API you use, with a token that panel issued. If this panel ever goes away, the node is still a panel someone can sign in to.

## How it fits together

```
            you sign in here
                  │
     ┌────────────▼────────────┐          ┌─────────────────────────┐
     │   panel  (server A)     │  API     │   node   (server B)     │
     │   customers, plans,     ├─────────►│   runs the tunnels you  │
     │   tunnels on A and B    │  every   │   created for it        │
     │   usage from both       │◄─────────┤   reports usage         │
     └─────────────────────────┘  30 s    └─────────────────────────┘
              ▲                                      ▲
              │  one subscription link               │
              └─────────── customer ─────────────────┘
                    gets a config for A and one for B
```

Every 30 seconds the panel sends the node what it should be running — tunnels, accounts, limits — and reads back what each account used. Nothing is a command: if the node was unreachable for an hour, the next round is simply the whole picture again, and its counters kept counting meanwhile, so no usage is lost.

## Before you start

You need:

- **Two servers**, each with a public IP. Server A is the panel you already have. Server B will be the node.
- On server B, **ports open**: the panel port you will choose (TCP), and the UDP/TCP ports of the tunnels you will create there.
- Server A must be able to **reach server B's panel port** over the internet (or a private link).

## Step 1 — install W-UI on the node

On server B, install exactly as you did on A:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh)
```

Answer the questions as usual. Two things matter for a node:

- **Get a certificate.** The default — Let's Encrypt for the server's own IP — is enough. This panel will check that certificate every time it talks to the node, and it is what stops someone on the path from reading the token. (A domain certificate works just as well.)
- **Write down the Access URL** it prints at the end, port and path included. That is the node's address.

You do not need to create anything on the node's own panel. Leave it empty; this panel fills it.

## Step 2 — issue a token on the node

The token is how this panel proves it may manage the node. Make it on the node, on either of these:

- **From the terminal on server B:**

  ```bash
  wui token issue --name "panel A"
  ```

- **Or from the node's own panel:** sign in at its Access URL → **Nodes** → **Issue token** → give it a name → copy it. (Tokens are also listed, disabled and revoked under **Settings → Authentication → API tokens**.)

The token is shown **once**. Copy it now; it is stored only as a hash and cannot be shown again. (If you lose it, issue another and revoke the old one under Settings → Authentication → API tokens on the node.)

## Step 3 — add the node here

On server A's panel: **Nodes** → **Add node**.

| Field | What to enter |
|---|---|
| **Name** | Anything you like — `Frankfurt`, `node-2`. It appears in the tunnel list and on the customers' config names. |
| **Address** | The node's Access URL as you would open it in a browser: `https://203.0.113.9:41873/`. The path after the port is not needed; the port is. |
| **Access token** | The token from step 2. |
| **Certificate check** | Leave on **Verify normally** when the node has a real certificate (Let's Encrypt for its IP or domain — the installer's default). See [the three modes](#certificate-check) below for the others. |
| **Usage multiplier** | `1`, unless this server should cost customers more or less — see [below](#usage-multiplier). |
| **Transfer allowance** | Only if the host caps the node's monthly traffic — see [below](#transfer-allowance). |

Save. Within a few seconds the row shows **Online**, the node's version, uptime, CPU, RAM and latency. **Check now** probes it on demand.

If it shows **Offline**, the status says which kind: *refused* (port closed or wrong port), *no answer* (firewall or wrong IP), *wrong credentials* (bad token), or *answered but is not a panel* (wrong address — something else is on that port). [Troubleshooting](#when-a-node-goes-quiet) has the checks.

## Step 4 — create a tunnel on the node

**Interfaces** → **Add inbound** → in the **Server** field pick the node instead of *this server*. Everything else is the same as a local tunnel: protocol, port, subnet, endpoint (the node's public IP or a name pointing at it).

The panel sends it to the node on the next round; the node brings it up. The row shows the server it lives on. A tunnel cannot be moved to another server later — every customer on it would lose their config — so pick the server first.

You can give the node several tunnels (WireGuard, AmneziaWG, OpenVPN), and hosts and host groups work on them exactly as on local ones.

## Step 5 — put customers on it

**Clients** → **Add client** → under **Servers this customer can use**, tick every tunnel the customer may use — on this server, on the node, or both. The allowance, the expiry and the device limit are the customer's, shared across all of them.

Each device the customer adds gets its own account on every tunnel it is allowed to reach, and the subscription link hands the app a config for each. Usage from every server adds up into the one allowance; when it runs out, the customer is cut off everywhere.

Bulk actions (attach existing customers to a tunnel, move a group) work across servers the same way.

### Connections at once, across servers

The plan's **connections at once** is the customer's, not a server's. Every three seconds the panel asks each node which credentials are live on it (a few bytes per session, not the whole state), adds that to what its own kernel sees, and counts the customer's connections across everything — a WireGuard file on this server and an OpenVPN login on the node are two. When more are connected than the plan allows, the newest is held off for two minutes: on this server directly; on a node, the panel tells the node at once (`/api/node/hold`) and again with every push, and the node ends the session and keeps the peer off until then. The panel's log says `connection limit reached; device held off … on="node 2"`, the node's says `device held off by the panel`.

If a node cannot be reached, its last report is believed for 30 seconds and then not counted — a customer's device on an unreachable node cannot be held through it either. On the node's side, once it has not heard from its panel for 45 seconds it holds customers to the limit on its own, with only what it can see, so a network blip between the servers is not a way past the limit. Both need this release or newer.

## Certificate check

The token lets this panel read every customer's keys on that node, so *who answers* at the address matters. Three modes:

| Mode | When |
|---|---|
| **Verify normally** | The node has a certificate a browser would accept — Let's Encrypt for its IP or its domain. This is the installer's default and the right choice. |
| **Accept one certificate only** (pinned) | The node has a self-signed or private certificate. Click **Read it** to fetch the fingerprint the address presents right now, and compare it with the node itself (`openssl x509 -in /etc/wui/certs/<name>/fullchain.pem -noout -fingerprint -sha256` on server B). This panel then accepts that certificate and no other, whoever signed it. |
| **Do not check at all** | Only on a private link nobody else can reach. Anyone between the two servers can read and change everything, the token included. |

### Proving who the panel is (mutual TLS, optional)

By default the node accepts any caller that knows the token. To make it accept **only this panel**, even if the token leaks from a log or a backup:

1. On server A: **Nodes** → **This panel's authority** → **Show it** → copy.
2. On server B's own panel: **Settings** → **Authentication** → **Panel allowed to manage this one** → paste → save.
3. On server A, edit the node and choose **Verify, and prove who this panel is**.

From then on the node refuses a request without this panel's client certificate (`this node only accepts a managing panel that presents a client certificate`). Clearing the field on the node turns it back off.

## Usage multiplier

What a gigabyte through this node costs the customer. `1` counts it as it is. `2` charges double — for a server whose bandwidth costs you more, or that you want to discourage. `0.5` counts half. It multiplies what the node reports before it is taken off the customer's allowance, so a 10 GB plan lasts 5 GB of traffic on a `2` node.

## Transfer allowance

If the host caps server B's monthly traffic, set the cap here and the day of the month it resets (1–28; `0` means you clear it by hand). When the node has carried that much, the panel takes every customer off it until the reset — the row says **allowance spent**. Customers with other servers keep working there.

## Updating a node

Update nodes when you update the panel: a node older than its panel still syncs (fields it does not know are ignored), but the connections limit across servers and the withdrawal of deleted tunnels need both sides on the same release.

**Nodes** → the node's row → **Update** (shown when a newer release exists). The panel *asks* the node to update itself — you confirm **Install and restart**; the node fetches the release from GitHub and checks the signature with the key built into its own binary. Nothing travels from this panel, so a compromised panel cannot push code onto nodes. Or, on server B: `w-ui update`.

## Removing a node

**Nodes** → remove. Nothing on server B changes: it keeps running its tunnels and its customers keep working until you delete them on the node's own panel. Its token stays valid until you revoke it there (Settings → Authentication → API tokens).

To delete the node's tunnels from here first, delete them under **Interfaces** — each round names the tunnels the node should still have, so a deleted one is taken down on the node, device and all, within a round.

## When a node goes quiet

The row says what kind of quiet. In order:

1. **Can server A reach it?** On A: `curl -sk https://NODE-IP:PORT/api/meta` — you should get JSON. A timeout is the network or a firewall on B (`ufw status`, the host's firewall panel); *connection refused* is the wrong port or the node's panel down (`w-ui status` on B).
2. **Wrong credentials.** The token was revoked or mistyped. Issue a new one on B and edit the node here.
3. **Certificate refused.** The node's certificate changed (renewed self-signed, or a new install) while the mode is *pinned*: read the fingerprint again. With *Verify normally*, an IP certificate that failed to renew: on B, `w-ui` → 20.
4. **Answered but is not a panel.** Something else answers at that address — check the port in the Access URL.
5. **Online but tunnels not appearing on B.** `journalctl -u wui -f` on A during a round shows `node …` lines with the reason; on B, the tunnel needs its port free and, for AmneziaWG, the module — the same messages as a local tunnel, listed in [Errors](/reference/errors).

While a node is unreachable the panel keeps every record; the customers on that server are the ones who notice. Everything they used meanwhile is counted when it comes back.

## The node's own panel

Server B stays a complete panel. You can sign in to it, run `w-ui` there, take backups of it, and see the tunnels and accounts this panel put on it — they are marked as managed and edited from here, not there. Its scheduled backups are its own; a restore of A does not restore B.
