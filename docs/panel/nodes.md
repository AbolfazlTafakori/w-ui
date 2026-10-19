---
description: "Sell one plan across several servers. What a node is, how to prepare the second server, how to add it, how customers get a configuration for every server, and what to do when a node goes quiet."
---

# Nodes: more than one server

A **node** is another server, added to this panel by its address. It can be a second W-UI, a 3x-ui panel or a Pasarguard panel. Once it is added you manage everything from here: you create tunnels *on* the node, put customers on them, and each customer's one subscription carries a configuration for every server. One plan, one allowance, several servers: when one is blocked, the rest keep working.

There is no separate agent to install. A W-UI node is a full W-UI panel, and this panel talks to it over the same API you use, with a token that panel issued. If this panel ever goes away, the node is still a panel someone can sign in to.

## How it fits together

```
            you sign in here
                  │
     ┌────────────▼────────────┐          ┌─────────────────────────┐
     │   panel  (server A)     │  API     │   node   (server B)     │
     │   customers, plans,     ├─────────►│   runs the tunnels you  │
     │   tunnels on A and B    │  every   │   created for it        │
     │   usage from both       │◄─────────┤   reports usage         │
     └─────────────────────────┘  20 s    └─────────────────────────┘
              ▲                                      ▲
              │  one subscription link               │
              └─────────── customer ─────────────────┘
                    gets a config for A and one for B
```

Every 20 seconds the panel sends the node what it should be running, its tunnels, accounts and limits, and reads back what each account used. None of it is a one-off command: if the node was unreachable for an hour, the next round simply sends the whole picture again, and the node's counters kept counting meanwhile, so no usage is lost.

## Before you start

You need:

- **Two servers**, each with a public IP address. Server A is the panel you already have; server B will be the node.
- **Open ports on server B**: the panel port you will choose (TCP), and the ports of the tunnels you will create there.
- **A path from A to B:** server A must reach server B's panel port over the internet or a private link.

## Step 1: install W-UI on the node

On server B, install exactly as you did on A:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh)
```

Answer the questions as usual. Two things matter for a node:

- **Get a certificate.** The default, Let's Encrypt for the server's own IP address, is enough, and a domain certificate works just as well. This panel checks that certificate every time it talks to the node, and it is what stops someone on the path from reading the token.
- **Write down the Access URL** it prints at the end, port and path included. That is the node's address.

You do not need to create anything on the node's own panel. Leave it empty; this panel fills it.

## Step 2: issue a token on the node

The token is how this panel proves it may manage the node. Make it on the node, in either of two ways:

- **From the terminal on server B:**

  ```bash
  wui token issue --name "panel A"
  ```

- **From the node's own panel:** sign in at its Access URL, go to **Nodes → Issue token**, give it a name and copy it. Tokens are also listed, disabled and revoked under **Settings → Authentication → API Token**.

The token is shown **once**. Copy it now: it is stored only as a hash and cannot be shown again. If you lose it, issue another and revoke the old one under Settings → Authentication → API Token on the node.

## Step 3: add the node here

On server A's panel, go to **Nodes → Add node**. The form has three parts: Server, Access and trust, and Cost and allowance.

| Field | What to enter |
|---|---|
| **Type** | **W-UI panel** for a node like this one. For the other two types, see [A 3x-ui panel as a node](#a-3x-ui-panel-as-a-node) and [A Pasarguard panel as a node](#a-pasarguard-panel-as-a-node). |
| **Name** | Anything you like, such as `Frankfurt` or `node-2`. It appears in the tunnel list and in the names of customers' configurations. |
| **Address** | The node's Access URL as you would open it in a browser, such as `https://203.0.113.9:41873/Xk3pQ9vL2mT8/`, with both the port and the secret path. Without the path the node answers *not found* and shows as not a panel. |
| **Allow an address inside this server's network** | Off by default. Every request to a node carries a token with full access to a panel, so an address typed by mistake that points inside this server's own network is refused. Turn it on only when the node really is on a private network you control. |
| **Access token** | The token from step 2. |
| **Certificate check** | Leave it on **Verify normally** when the node has a real certificate, Let's Encrypt for its IP address or domain, which is the installer's default. See [the three modes](#certificate-check) for the others. |
| **TLS name (SNI)** | Optional. The name to ask for in the TLS handshake when the address is an IP. Set it when the node's certificate is for a domain, or when the network between the servers resets TLS connections that carry no name, as some filtered networks do. |
| **Usage multiplier** | `1`, unless this server should cost customers more or less; see [below](#usage-multiplier). |
| **Transfer allowance (GB)** | Only if the host caps the node's monthly traffic; see [below](#transfer-allowance). |

Save. Within a few seconds the row shows **Online**, with the node's version, uptime, CPU, RAM and latency. **Check now** probes it on demand.

If it shows **Offline**, the status says which kind: *refused* (the port is closed or wrong), *no answer* (a firewall or a wrong IP address), *wrong credentials* (a bad token), or *answered but is not a panel* (a wrong address, with something else on that port). [When a node goes quiet](#when-a-node-goes-quiet) has the checks.

## The Nodes page

The strip at the top shows how many **Nodes** there are, how many are **Online** and **Offline**, and the **Average latency** to them. Beside **Add node** are **This panel's authority**, for [mutual TLS](#proving-who-the-panel-is-mutual-tls-optional), and **Issue token**, for when *this* panel is to be managed as a node.

| Column | What it shows |
|--------|---------------|
| **Actions** | ⚡ **Check now** probes the node at once; ⤓ **Ask this node to update itself** (see [Updating a node](#updating-a-node)); for a 3x-ui or Pasarguard node, **Bring in users**; ✎ edits; 🗑 removes. |
| **Enabled** | A switch. A switched-off node is not synced, and no new tunnel can be put on it. |
| **Name** | The node's name, with its type and how its certificate is checked. This server's own row is marked as this panel. |
| **Address** | Where it is reached. |
| **Status** | **Online** or **Offline** with the reason, and **allowance spent** when its transfer allowance is used up. This server reads **Running**. |
| **CPU** · **RAM** · **Version** · **Uptime** | As the node reports them. |
| **Transfer** | What it has carried this period, against its allowance when one is set. |
| **Latency** · **Last seen** | The last check's round trip, and when the node last answered. |

On a phone the table becomes a card per node; tap one to see its readings, and ⋮ holds the same actions.

## Step 4: create a tunnel on the node

Go to **Interfaces → Add Interface** and, in the **Server** field, pick the node instead of *this server*. Everything else is as for a tunnel here: the protocol, port, subnet and endpoint, which is the node's public IP address or a name pointing at it.

The panel sends the tunnel to the node in the next round, and the node brings it up. The row shows which server the tunnel lives on. A tunnel cannot be moved to another server later, since every customer on it would lose their configuration, so pick the server first.

A W-UI node can carry several WireGuard, AmneziaWG and OpenVPN tunnels, and one each of OpenConnect, IKEv2, L2TP/IPsec and PPTP; those four need W-UI 3.0 or later on the node. Hosts and host groups work on a node's tunnels exactly as on this server's.

## Step 5: put customers on it

Go to **Clients → Add Clients** and, under **Servers this customer can use**, tick every tunnel the customer may use: on this server, on the node, or both. The allowance, the end date and the number of users are the customer's, shared across all of them.

Each file the customer holds gets its own account on every tunnel it is allowed to reach, and the subscription link gives the app a configuration for each. Usage from every server adds up into the one allowance, and when it runs out, the customer is cut off everywhere.

Bulk actions, such as attaching existing customers to a tunnel or moving a group, work across servers the same way.

### Connections at once, across servers

The plan's **Users**, the connections it allows at once, are the customer's, not a server's. Every three seconds the panel asks each node which sign-ins are live on it, a few bytes per session rather than the whole state, adds that to what its own kernel sees, and counts the customer's connections across everything; a WireGuard file on this server and an OpenVPN sign-in on the node are two. When more are connected than the plan allows, the newest is held off for two minutes. On this server that happens directly. On a node, the panel tells the node at once (`/api/node/hold`) and again with every round, and the node ends the session and keeps the device off until then. The panel's log says `connection limit reached; device held off … on="node 2"`, and the node's says `device held off by the panel`.

If a node cannot be reached, its last report is believed for 30 seconds and then not counted, so a customer's device on an unreachable node cannot be held through it either. On the node's side, once it has not heard from its panel for 45 seconds, it holds customers to the limit on its own, with what it can see, so a network blip between the servers is not a way past the limit. Both sides need this release or newer.

## A 3x-ui panel as a node

A server running 3x-ui, version 2.0 or later, can be a node without installing W-UI on it. Before 3.0 it is reached by username and password; versions 3.0 to 3.2 by an API token, with the username and password as well for the outbound (and for links on 3.0); 3.3 and later by an API token alone.

1. On the 3x-ui panel, version 3 or later, make an API token under **Settings → API tokens**.
2. Here, use **Add node**, choose the type **3x-ui panel**, and give its access URL with the secret path (`https://IP:port/path`, as 3x-ui prints it) and the token, or the username and password.
3. Within twenty seconds each inbound appears on the Interfaces page under the node's tab, with its protocol, port and transport. Put customers on them as on any tunnel.

What happens then:

- **Each file is a client on the 3x-ui panel**, named `wui-` and a number. Clients that panel's own operator made are never touched.
- **Usage is read from the 3x-ui counters every twenty seconds** and added to the customer's one total, at the node's usage multiplier.
- **The allowance and expiry are this panel's.** The client there has no limit of its own; when the customer runs out or expires here, it is switched off there, and on again when topped up.
- **One user per file** is the client's IP limit there, 1, enforced by 3x-ui. A customer with no device limit has none there either.
- **The links are the ones 3x-ui makes** for the client, with its address, keys and transport, and they go into the customer's one subscription. When the inbound changes there, the links follow within a round.
- **Inbounds are edited on the 3x-ui panel,** not here. An inbound removed there is shown as gone and leaves the subscription, and its tunnel goes once no customer is on it.
- **Speed limits do not apply** on a 3x-ui node, because xray has no rate per user.

## A Pasarguard panel as a node

A Pasarguard panel, version 1.0 or later, with a node of its own connected, can be a node the same way: by an administrator's username and password, or from version 5.2 by an API key.

1. On the Pasarguard panel, make an API key under **API keys**, or use an administrator's username and password.
2. Here, use **Add node**, choose the type **Pasarguard panel**, and give its address (`https://IP-or-domain:port`) and the key, or the username and password.
3. Within twenty seconds each inbound appears on the Interfaces page under the node's tab.

What happens then is as for 3x-ui, in Pasarguard's own way:

- **Each file is a user** there named `wui_` and a number, in a group this panel keeps for that inbound alone (`wui <inbound>`). Users that panel's own operator made are never touched.
- **Their credentials are Pasarguard's,** and the links are the ones its subscription gives for the user.
- **Usage is its users' used traffic,** read every twenty seconds. The allowance and expiry are this panel's, and a user is disabled there when the customer runs out here.
- **Pasarguard has no IP limit of its own,** so one user per file is not enforced on such a node.
- **A Shadowsocks-2022 inbound has a standing user**, `wuikeep_` and a number, whose key nobody is given. A Pasarguard node that starts such an inbound with no user takes none on it afterwards, and the sync of every user on that node fails, its operator's own included. Leave that user in place.

Names made on a node carry this installation's six-letter tag (`wui-<tag>-<n>` on 3x-ui, `wui_<tag>_<n>` on Pasarguard), so two W-UI panels on one node never remove each other's users.

## Bringing in a panel's own users

A 3x-ui or Pasarguard panel usually has users before it becomes a node. Right after it is added, and any time later from **Bring in users** on its row, its users are listed with their traffic, expiry and inbounds. Bring in all of them, or the ones you tick. The node's inbounds must already show under its tab, which takes up to twenty seconds after adding it.

- **Nothing changes for the user.** Its credentials, its links and the subscription its owner gave it stay as they are and keep working, with no interruption. The customer's subscription here carries the same links, alongside whatever else they are sold.
- **Its allowance, usage, expiry and IP limit come with it** and are this panel's from then on. Its own limits on that panel are cleared, so that panel never ends it on numbers this one no longer keeps; it is switched off there when it runs out here, and on again when it is renewed.
- **A user switched off there by hand stays off,** here too. **A customer removed here is left there** as it was.
- On **3x-ui**, the clients sharing one subscription ID are one customer. On **Pasarguard**, a user is one customer; its groups stay its owner's, and its links carry every inbound in them.
- Users made there later are not taken over on their own: bring them in from the same button. Users a W-UI panel made are never offered.

## Making an inbound on a 3x-ui or Pasarguard node

On the Interfaces page, use **Add Interface** and choose the server first. The protocol list then holds what that panel offers at its version:

| Panel | Protocols |
|---|---|
| 3x-ui | VLESS, VMess, Trojan, Shadowsocks, HTTP (2.0); WireGuard, SOCKS, dokodemo-door (2.5); mixed and tunnel in place of those two (2.8); Hysteria2 (2.9); MTProto (3.4); TUIC, AmneziaWG (3.8); MASQUE (3.10) |
| Pasarguard | VLESS, VMess, Trojan, Shadowsocks, Hysteria2, WireGuard, mixed, SOCKS, HTTP, tunnel, dokodemo-door |

The form asks what that panel's own form asks: each protocol's settings, the transport (TCP, WebSocket, gRPC, HTTPUpgrade, XHTTP, mKCP), TLS from a certificate file or text, Reality, fallbacks and sniffing; on 3x-ui also the inbound's own allowance, expiry and traffic reset, and on Pasarguard the host its links are written from. Keys for Reality, WireGuard, Shadowsocks 2022 and MTProto are drawn here.

If that panel's xray does not come back running with the new inbound, the inbound is taken out again and the reason shown, so its operator's own users never stop. SOCKS, HTTP, mixed and tunnel inbounds are made but take no customers, since they have no account per user.

## Certificate check

The token lets this panel read every customer's keys on that node, so *who answers* at the address matters. There are three modes:

| Mode | When |
|---|---|
| **Verify normally** | The node has a certificate a browser would accept: Let's Encrypt for its IP address or its domain. This is the installer's default and the right choice. |
| **Accept one certificate only** | The node has a self-signed or private certificate. Click **Read it** to fetch the fingerprint the address presents right now, and compare it with the node itself: `openssl x509 -in /etc/wui/certs/<name>/fullchain.pem -noout -fingerprint -sha256` on server B. This panel then accepts that certificate and no other, whoever signed it. |
| **Do not check at all** | Only on a private link nobody else can reach. Anyone between the two servers can read and change everything, the token included. |

### Proving who the panel is (mutual TLS, optional)

By default the node accepts any caller that knows the token. To make it accept **only this panel**, even if the token leaks from a log or a backup:

1. On server A, go to **Nodes → This panel's authority → Show it** and copy it.
2. On server B's own panel, go to **Settings → Authentication → Panel allowed to manage this one**, paste it and save.
3. On server A, edit the node and choose **Verify, and prove who this panel is**.

From then on the node refuses any request without this panel's client certificate (`this node only accepts a managing panel that presents a client certificate`). Clearing the field on the node turns this off again.

## Usage multiplier

What a gigabyte through this node costs the customer. `1` counts it as it is. `2` charges double, for a server whose bandwidth costs you more or that you want customers to use less. `0.5` counts half. It multiplies what the node reports before it is taken off the customer's allowance, so a 10 GB plan lasts 5 GB of traffic on a node at `2`. Each device's row in the customer's usage table is multiplied the same way, so the rows still add up to the usage, to the byte.

## Transfer allowance

If the host caps server B's monthly traffic, set the cap here and the day of the month it resets, **Resets on day**: 1 to 28, or `0` to clear it by hand. When the node has carried that much, the panel takes every customer off it until the reset, and the row says **allowance spent**. Customers with other servers keep working there.

## Updating a node

Update nodes when you update the panel. A node older than its panel still syncs, ignoring fields it does not know, but the connection limit across servers, the withdrawal of deleted tunnels and the protocols new in 3.0 need both sides on the same release.

On the node's row, use ⤓ **Ask this node to update itself** and confirm. The panel *asks* the node to update itself; the node fetches the release from GitHub and checks its signature with the key built into its own binary. Nothing travels from this panel, so a compromised panel cannot push code onto nodes. Or, on server B, run `w-ui update`.

::: warning Nodes on v2.0.0 to v2.6.0
A node on any of these releases refuses its managing panel's token. It shows as unreachable, customers added here never reach it, its traffic is not counted, and the update button cannot reach it either. Run `w-ui update` once on the node server itself. From v2.6.1 on, the node is managed, and updated, from here again.
:::

## Removing a node

Remove it from the **Nodes** page. Nothing on server B changes: it keeps running its tunnels, and its customers keep working until you delete them on the node's own panel. Its token stays valid until you revoke it there, under Settings → Authentication → API Token.

To delete the node's tunnels from here first, delete them under **Interfaces**. Each round names the tunnels the node should still have, so a deleted one is taken down on the node, device and all, within a round.

## When a node goes quiet

The row says what kind of quiet it is. Check these in order:

1. **Can server A reach it?** On A, run `curl -sk https://NODE-IP:PORT/api/meta`; you should get JSON. A timeout means the network or a firewall on B (`ufw status`, or the host's firewall panel). *Connection refused* means the wrong port, or the node's panel is down (`w-ui status` on B).
2. **Wrong credentials.** The token was revoked or mistyped. Issue a new one on B and edit the node here.
3. **Certificate refused.** In *Accept one certificate only* mode, the node's certificate changed, through a renewed self-signed certificate or a new install: read the fingerprint again. In *Verify normally* mode, an IP certificate failed to renew: on B, open `w-ui` → 20.
4. **Answered but is not a panel.** Something else answers at that address; check the port in the Access URL.
5. **Online, but the tunnels do not appear on B.** Run `journalctl -u wui -f` on A during a round: `node …` lines give the reason. On B, the tunnel needs its port free and, for AmneziaWG, the module; the messages are the same as for a tunnel here, and are listed in [Errors](/reference/errors).

While a node is unreachable, the panel keeps every record; only the customers on that server notice. Everything they used meanwhile is counted when it comes back.

## The node's own panel

Server B stays a complete panel. You can sign in to it, run `w-ui` there, take backups of it, and see the tunnels and accounts this panel put on it; they are marked as managed, and are edited from here, not there. Its scheduled backups are its own, and restoring A does not restore B.
