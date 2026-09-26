---
description: "Selling the panel on. A reseller signs in with their own username and password, sees only the customers they created, on the servers you allow, within a ceiling of customers, traffic and time."
---

# Operators

**Settings → Operators**, and the owner's alone.

A panel with more capacity than customers gets sold on. An operator row is how: the person you sell to signs in here with their own username and password, manages their own customers, and never reaches the machine underneath.

## The three roles

| | Owner | Panel administrator | Reseller |
|---|---|---|---|
| Interfaces, nodes, hosts, outbounds, routing, engine, settings, backups | ✅ | — | — |
| Operators | ✅ | — | — |
| Clients and Groups | every one | every one | their own only |
| Ceiling: customers, traffic, expiry | — | — | ✅ |
| Menu they see | everything | Overview, Clients, Groups | Clients, Groups |

There is exactly one owner. It cannot be deleted, switched off, or handed over from this page.

A **panel administrator** is a second pair of hands over the customers — every customer on the panel, no ceiling — with nothing about the server itself. A **reseller** is a customer of yours who sells on.

## Adding a reseller

You choose:

- **Username and password.** They sign in with these, at the same address you do.
- **Servers.** The tunnels they may put a customer on. Choose nothing and they cannot sell at all. A machine reserved for one reseller (Nodes → owner) carries nobody else.
- **Customer limit.** How many customers they may hold at once. Zero is no limit.
- **Traffic allowance.** What their customers may carry *between them* — measured in what was actually used, not in what was promised, so ten unlimited plans nobody connects to cost nothing. Zero is no limit.
- **Until.** The date their own term ends. Empty is no end.

Every customer they create is filed under a group of yours, named after them, that they are never shown and cannot rename, empty or leave. Filtering the Clients page by it is how you see whose customers are whose.

## Switching one off

Switching a reseller off stops every customer they hold, at once — and the same happens on its own when their allowance runs out or their term ends.

Nothing is written to those customers. Their own switches are left exactly as they were, so switching the reseller back on (or taking the next month's payment with *start their allowance again*) restores precisely the service that was running, rather than reviving plans that were stopped on purpose.

The reseller's open sessions end the moment they are switched off, and again whenever you set a new password for them.

## Removing one

You are asked what becomes of their customers, because both answers are expensive to get wrong:

- **Keep them, under me** — the plans stay live and every configuration people are holding keeps working. Only who administers them changes.
- **Delete them too** — what the reseller sold stops, and their addresses go back to the pools.

Either way a node that was reserved for them goes back to the shared pool rather than being deleted: the machine is still there and still costs money.

## What holds this up

The separation is not the menu. Every endpoint checks for itself, and a reseller's reads and writes are narrowed to their own customers by the database, on a rule registered once rather than a condition written out per query — so a path nobody has thought of yet is narrowed too.
