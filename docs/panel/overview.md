---
description: "The first page after sign-in: how the customers are doing, how loaded the server is, and the buttons that act on the whole server: restart, stop, update, history, logs and backup."
---

# Overview

The first page after sign-in. It answers two questions, in this order: does anything need doing for the customers today, and how is the server doing. It refreshes every 3 seconds from one request, so every number on it is from the same moment.

## The action bar

At the start of the bar is what this server is running; at the end are the buttons.

| Item | What it does |
|------|--------------|
| **● Tunnels · 3 / 3** | How many interfaces on this server are carrying traffic, out of how many exist. The dot is green when all are up and red when one is not. |
| **v3.0.0** (the version) | This panel's version. Click it to open the [update window](#updating). |
| **Update** | Shown only while a newer release is published; it opens the update window. During an install it reads **Updating…**. |
| **Restart** | Reopens every tunnel on this server from its stored settings. No record changes, and customers reconnect within seconds. |
| **Stop** | Switches every tunnel off, after a confirmation. Every customer on this server is disconnected and cannot reconnect until the tunnels are on again, and they stay off across a restart of the panel or the server. Nothing is deleted and no allowance changes. Once the tunnels are stopped, the button becomes **Start**, which switches them back on. |
| **History** | Opens the [history](#history) window. |
| **Logs** | Opens the [log](#the-log) window. |
| **Backup & Restore** | Opens the [backup window](#backup-and-restore). |
| **System** | The system report, under *Settings → System*: what this program is, what the kernel supports, and whether limits are enforced exactly. |
| **Settings** | Opens the [settings](/panel/settings). |

On a narrow screen the buttons show only their icons.

## Customer tiles

Six tiles, each a link to the Clients page already filtered to it:

| Tile | Counts | Opens |
|------|--------|-------|
| **Online now** | Customers with traffic moving | Clients filtered to Online |
| **Depleting** | Active customers at 80% or more of their allowance | Clients filtered to Depleting |
| **Out of data** | Customers who used their whole allowance | Clients filtered to Out of data |
| **Expired** | Customers past their end date | Clients filtered to Expired |
| **Active** | Customers whose plan is running | Clients filtered to Active |
| **Clients** | Every customer | The whole list |

A tile with nothing to report is dimmed. The three that call for action, Depleting, Out of data and Expired, stand out while they are above zero.

## Server vitals

Four tiles, each showing a level beside its own recent history, with a small chart and the lowest and highest values on it:

| Tile | Shows |
|------|-------|
| **CPU** | Processor use, and how many cores |
| **RAM** | Memory use, as used / total |
| **Swap** | Swap use, as used / total, or *None configured* |
| **Storage** | Use of the disk holding the panel's data, as used / total |

Below them:

- **Throughput:** traffic across every network interface of this host, upload and download as a live chart, with how much was sent and received and the peak.
- **Connections:** open connections, by protocol.
- **Uptime:** of the panel and of the host, with the panel's own memory and **Tasks**, how many it is running.
- **Server addresses:** the server's public IPv4 and IPv6. They are hidden behind the eye until you show them, so a screenshot does not give them away.

## Interfaces

A short table of the interfaces on this server: the name, protocol, endpoint and port, the mode (Standard or AmneziaWG), the subnet, and the **Address pool**, how many addresses are handed out of how many there are, with a bar. Above it are the totals and the active customers out of all. With no interface yet, the table offers a link to create one.

## Updating

The update window shows the version running and the newest one published.

- **Install and restart** downloads the release, checks its signature against the key built into this panel, puts it in place and restarts. A bar shows the download, and the page reloads by itself once the panel is back on the new version. Customers stay connected, because the tunnels live in the kernel and outlive the panel. Closing the window or reloading the page does not stop an install.
- A build without a signing key refuses to update at all, and says so. A panel that installed an unverified program would be worse than one that does not update.
- The panel does not run as root and cannot replace its own program. The installer sets up a small root helper, `wui-update.path`, that checks the release again with the installed panel's key, refuses anything that is not a newer signed release, puts it in place and restarts the panel. A server installed before 2.3.2 has no helper. The window then says so and gives the one command to run as root, after which updates install from here.
- The list of releases is checked at most every half hour; opening the window checks again.

[Nodes](/panel/nodes) are updated from the Nodes page, or on each node with `w-ui update`.

## History

What this server has been doing: **Processor**, **Memory**, **Network**, **Connections**, **Storage used** and **Load average**, over 5 minutes, 1 hour, 6 hours, 24 hours, 48 hours or 7 days. Each chart shows its peak. The last hour is exact; longer ranges are averaged.

## The log

**Logs** opens the panel's log without leaving the page.

| Control | What it does |
|---------|--------------|
| Search | Matches the message or any field, as you type. |
| Level | Everything, Info and above, Warnings and errors, or Errors only. |
| Lines | How many lines to show. |
| Source | **Panel buffer**: the lines this process wrote, the last few thousand, kept in memory and empty after a restart. Or **System journal**: the same panel's lines on disk, which survive a restart. |
| Follow | Keeps showing new lines as they arrive. |
| ↻ · Copy · Download | Reload, copy what is shown, or save it as a file. |

What the log holds, one line each:

| Line | Fields | When |
|------|--------|------|
| `action` / `action refused` / `action failed` | `action` (method and path), `by` (the administrator, or `token`), `ip`, `status`, `took`, and for a refusal `reason` | Every change made through the panel or the API |
| `client created` / `client updated` / `client deleted` | `name`, `id`; for an edit, `changed` names what changed | The change itself, beside the action that asked for it |
| `device connected` / `device disconnected` | `device` as `customer / device`, `from` (the public address), `after` (how long it was on) | A file's traffic starting, and stopping for 75 seconds |
| `connection limit reached; device held off` / `device held off by the panel` | `client`, `device`, `on` (here or the node), `limit`, `until` | A plan over its number of users |
| `clients cut off for reaching their allowance` / `clients expired` | `count`, `clients` (the names) | The panel's check, every 2 seconds |
| `plans renewed: traffic back to zero` | `count`, `clients` | A plan's renewal period coming round |
| `admin signed in` / `failed sign-in` / `failed second factor` | `username`, `ip`, `lockout` | The sign-in page |
| `interface created` / `deleted` / `restarted`, `driver state changed` | `name`, `port` | Interfaces and their drivers |
| `node …` | `node`, `error` | Only when a node's state changes |

Reading is not logged, since opening a list is not an event. Nor is the traffic between a panel and its nodes.

## Backup and restore

| Line | What it does |
|------|--------------|
| **Download backup** | Takes a fresh archive of everything on this server, the database with every interface's keys and every customer, and saves it to your device. |
| **Choose a file** | Picks an archive to restore, from this server or another. Nothing changes yet: the window shows the file's name and size and what will happen. |
| **All backups →** | Opens *Settings → Backups*: the archives kept on the server, and how often one is taken and how many are kept. |

Restoring replaces everything on this panel with the archive's contents and restarts it. The customers, keys and administrators come from the archive, so afterwards you sign in with the archive's administrator. How this panel is reached, its port, URL path and certificate, stays this server's. What is here now is saved first, so a restore can be undone from the list of backups.

**Keep this server's addresses:** the archive names the server it was taken on. Leave this on when moving to a new server, so customers' configurations point here. Turn it off to make one server an exact copy of another.

The upload shows its progress, and the page reloads by itself once the panel is back with the restored data. See [Backup and restore](/operations/backup-restore).
