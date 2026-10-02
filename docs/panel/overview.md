---
description: "The first page after sign-in: how the customers are doing, how loaded the server is, and the buttons that act on the whole server — restart, stop, update, history, logs, backup."
---

# Overview

The first page after sign-in. It answers two questions in order: does anything need doing for the customers today, and how is the server doing. It refreshes every 3 seconds from one request, so every number on it is from the same moment.

## The action bar

At the start, what this server is running; at the end, the buttons.

| Item | What it does |
|------|--------------|
| **● Tunnels · 3 / 3** | How many interfaces on this server are carrying traffic, out of how many exist. The dot is green when all are up, red when one is not. |
| **v2.6.3** (the version) | This panel's version. Click it to open the [update window](#updating). |
| **Update v…** | Only while a newer release is published: opens the update window. While an update installs it reads **Updating…**. |
| **Restart** | Reopens every tunnel on this server from its stored settings. No record changes; customers reconnect within seconds. |
| **Stop** | Switches every tunnel off, after a confirmation. Every customer on this server is disconnected and cannot reconnect until the tunnels are switched back on — they stay off across a restart of the panel or the server. Nothing is deleted and no allowance changes. Once stopped, the button becomes **Start**, which switches them back on. |
| **History** | The [history](#history) window. |
| **Logs** | The [log](#the-log) window. |
| **Backup & Restore** | The [backup window](#backup-and-restore). |
| **System** | The system report under *Settings → System*: what this program is, what the kernel supports, whether limits are enforced exactly. |
| **Settings** | The [settings](/panel/settings). |

On a narrow screen the buttons show only their icons.

## Customer tiles

Six tiles, each a link to the Clients page already filtered to it:

| Tile | Counts | Opens |
|------|--------|-------|
| **Online now** | customers with traffic moving | Clients filtered to Online |
| **Depleting** | active customers at 80 % or more of their allowance | Clients filtered to Depleting |
| **Out of data** | customers who used their allowance | Clients filtered to Out of data |
| **Expired** | customers past their date | Clients filtered to Expired |
| **Active** | customers whose plan is running | Clients filtered to Active |
| **Clients** | every customer | the whole list |

A tile with nothing to report is dimmed; the three that need action (Depleting, Out of data, Expired) stand out while they are above zero.

## Vitals

Four tiles, each a level read against its own recent history, with a small chart and the minimum and maximum over that chart:

| Tile | Shows |
|------|-------|
| **CPU** | processor use, and how many cores |
| **RAM** | memory use, used / total |
| **Swap** | swap use, used / total — or *None configured* |
| **Storage** | disk use of the panel's data disk, used / total |

Then:

- **Throughput** — traffic across every network interface of this host, upload and download as a live chart, with how much was sent and received and the peak.
- **Connections** — open connections, by protocol.
- **Uptime** — of the panel and of the host; the panel's own memory and **Tasks** (how many it is running).
- **Server addresses** — the server's public IPv4 and IPv6. Hidden by default behind the eye, so a screenshot does not give them away.

## Interfaces

A short table of the interfaces on this server: name, protocol, endpoint and port, mode (Standard or AmneziaWG), subnet and **Address pool** — addresses handed out of how many there are, with a bar. Above it, the totals and the active customers out of all. With no interface yet, a link to create one.

## Updating

The update window shows the version running and the newest one published.

- **Install and restart** downloads the release, checks its signature against the key built into this panel, puts it in place and restarts. A bar shows the download; the page reloads by itself once the panel is back on the new version. Customers stay connected: the tunnels live in the kernel and outlive the panel. Closing the window or reloading the page does not stop an install.
- A build without a signing key refuses to update at all, and says so: a panel that installed an unverified program would be worse than one that does not update.
- The panel does not run as root and cannot replace its own program. The installer sets up a small root helper (`wui-update.path`) that checks the release again with the installed panel's key, refuses anything that is not a newer signed release, puts it in place and restarts the panel. A server installed before 2.3.2 has no helper: the window says so and gives the one command to run as root, after which updates install from here.
- The release list is checked at most every half hour; opening the window checks again.

[Nodes](/panel/nodes) are updated from the Nodes page, or on each node with `w-ui update`.

## History

What this server has been doing: **Processor**, **Memory**, **Network**, **Connections**, **Storage used** and **Load average**, over 5 minutes, 1 hour, 6 hours, 24 hours, 48 hours or 7 days. Each chart shows its peak. The last hour is exact; longer ranges are averaged.

## The log

**Logs** opens the panel's log without leaving the page.

| Control | What it does |
|---------|--------------|
| Search | Matches the message or any field, as you type. |
| Level | Everything, Info and above, Warnings and errors, Errors only. |
| Lines | How many lines to show. |
| Source | **Panel buffer** — the lines this process wrote, the last few thousand, kept in memory and empty after a restart; or **System journal** — the same panel's lines on disk, which survive a restart. |
| Follow | Keeps showing new lines as they arrive. |
| ↻ · Copy · Download | Reload, copy what is shown, save it as a file. |

What is in it, one line each:

| Line | Fields | When |
|------|--------|------|
| `action` / `action refused` / `action failed` | `action` (method and path), `by` (the administrator, or `token`), `ip`, `status`, `took`, and for a refusal `reason` | every change made through the panel or the API |
| `client created` / `client updated` / `client deleted` | `name`, `id`; for an edit `changed` names what changed | the change itself, beside the action that asked for it |
| `device connected` / `device disconnected` | `device` as `customer / device`, `from` (the public address), `after` (how long it was on) | a file's traffic starting, and stopping for 75 seconds |
| `connection limit reached; device held off` / `device held off by the panel` | `client`, `device`, `on` (here or the node), `limit`, `until` | a plan over its users |
| `clients cut off for reaching their allowance` / `clients expired` | `count`, `clients` (names) | the panel's check, every 2 seconds |
| `plans renewed: traffic back to zero` | `count`, `clients` | a plan's renewal period coming round |
| `admin signed in` / `failed sign-in` / `failed second factor` | `username`, `ip`, `lockout` | the sign-in page |
| `interface created` / `deleted` / `restarted`, `driver state changed` | `name`, `port` | interfaces and their drivers |
| `node …` | `node`, `error` | only when a node's state changes |

Reading is not logged: opening a list is not an event. Nor is the traffic between a panel and its nodes.

## Backup and restore

| Line | What it does |
|------|--------------|
| **Download backup** | Takes a fresh archive of everything on this server — the database, every interface's keys, every customer — and saves it to your device. |
| **Choose a file** | Picks an archive to restore, from this server or another. Nothing changes yet: the window shows its name and size and what will happen. |
| **All backups →** | The archives kept on the server (see *Settings → Backup*). |

Restoring replaces everything on this panel with the archive's contents and restarts it. Its customers, keys and administrators come from the archive — afterwards you sign in with the archive's administrator. How this panel is reached (its port, URL path and certificate) stays this server's. What is here now is saved first, so a restore can be undone from the list of backups.

**Keep this server's addresses**: the archive names the server it was taken on. Leave this on when moving to a new server, so customers' configurations point here. Turn it off to make one server an exact copy of another.

The upload shows its progress, and the page reloads by itself when the panel is back with the restored data. See [Backup and restore](/operations/backup-restore).
