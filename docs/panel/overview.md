---
description: "The first page after sign-in: host load, panel state, and what this server is carrying. It refreshes every three seconds from one request, so every number is from the same moment."
---

# Overview

The first page after sign-in: host load, panel state, and what this server is carrying. It refreshes every three seconds from one request, so every number is from the same moment.

## The action bar

| Button | What it does |
|--------|--------------|
| **Tunnels 3 / 3** | how many interfaces are up, of how many are enabled; red when one is down |
| **Restart** | every tunnel again from its stored configuration (the classic "restart core"); the panel stays up |
| **Stop** | the panel service — tunnels keep running, limits stop being enforced |
| **History** | CPU, memory, traffic and connections over the last day, week or month |
| **Logs** | the panel's log, filterable by level and source, live |
| **Backup** | take one now, download one, restore one |
| **System** | what this binary is, what the kernel supports, whether enforcement is exact |
| **Settings** | the settings pages |

## The strip

Online now, depleting, out of data, expired, active, and the number of customers — each one a link to the Clients page filtered to it.

## Vitals

CPU, RAM, swap and storage with a sparkline; throughput across every interface with the last hour's peak; open connections by protocol. Under them, uptime of the panel and of the host, the panel's own memory and tasks, and the server's public addresses — hidden by default behind the eye, for screenshots.

## Enforcement

The line that matters commercially. **Exact (kernel quota)** means data limits are enforced by nftables quota objects and the overshoot is one packet. **Reduced** means this kernel has no `nft_quota`, limits are applied on the next poll, and a fast customer overshoots — see [How enforcement works](/reference/how-it-works).
