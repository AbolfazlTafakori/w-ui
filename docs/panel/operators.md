---
description: "Selling the panel on. A reseller signs in with their own username and password, sees only the customers they created, on the servers you allow, within a ceiling of customers, traffic and time."
---

# Resellers

**Resellers**, in the main menu, and the owner's alone.

A panel with more capacity than customers gets sold on. A reseller is how: the person you sell to signs in here with their own username and password, manages their own customers, and never reaches the machine underneath.

## The three roles

| | Owner | Panel administrator | Reseller |
|---|---|---|---|
| Interfaces, nodes, hosts, outbounds, routing, engine, settings, backups | ✅ | — | — |
| Resellers | ✅ | — | — |
| Clients and Groups | every one | every one | their own only |
| Ceiling: customers, traffic, time | — | — | ✅ |
| Menu they see | everything | Overview, Clients, Groups | Clients, Groups |

There is exactly one owner. It cannot be deleted, switched off, or handed over, and it is not listed on the Resellers page.

A **panel administrator** is a second pair of hands over the customers — every customer on the panel, no ceiling — with nothing about the server itself. A **reseller** is a customer of yours who sells on.

## The page

Across the top: how many resellers you have, how many are active, ending soon or stopped, how many customers they hold between them, and how much traffic those customers have used.

A row each, in the same colours as the customer list — green while there is room, orange when it is running low, red when it has run out, purple for unlimited:

- **Customers** — how many they hold against their limit.
- **Traffic** and **Traffic left** — what their customers have used, and what is left of the allowance.
- **Time left** — days until their term ends, or *on hold* while it has not started.
- **Servers** — the ones they may sell.

On a phone each reseller is a card with the same figures.

## Adding a reseller

The dialog is laid out as the customer dialog is.

- **Username and password** are drawn for you; the button beside each draws another. The copy button puts the panel's address, the username and the password on the clipboard together, ready to send.
- **Customers.** How many they may hold at once. Empty is no limit.
- **Traffic.** What their customers may carry *between them* — measured in what was actually used, not what was promised, so ten unlimited plans nobody connects to cost nothing. Empty is no limit.
- **Until.** The last day their account works, picked from a calendar in the calendar the panel is set to (Gregorian, or Jalali — Settings → General → Calendar Type). *+1 month*, *+3 months* and *+1 year* count from the date already set, so extending is one click.
- **On hold.** The term is a number of days that starts the first time they sign in, not today: sold on Friday and first signed in on Monday, they still get every day.
- **Group.** What their customers are filed under on your own customer list, so you can see whose they are. It defaults to `reseller:<username>`; type a name to use another, or an existing group of yours. They never see it. Two resellers cannot share one — the list could no longer tell them apart.
- **Servers.** The tunnels they may put a customer on. Their own customer form offers exactly these: nothing more, and nothing missing. Choose nothing and they cannot sell at all. A machine reserved for one reseller (Nodes → owner) carries nobody else.

The presets fill a typical reseller plan in one click.

Changing the group later moves all their existing customers to the new name.

## What the reseller sees

Their Clients page opens with **Your account**: customers against their limit, traffic left, time left. If their term has ended or their allowance is used up it says so, and why.

When you take a server back, the customers already on it keep working, and the reseller can still rename, extend or take them off it. They cannot put anyone new there.

## Switching one off

Switching a reseller off stops every customer they hold, at once, signs them out, and keeps them out: they cannot sign in again until you switch them back on.

A reseller whose term has ended or whose allowance is used up is stopped the same way, but may still sign in — to see why, and to pay. They can read their customers and change nothing: no new customers, no extending, no topping up, nothing that would come into effect the moment they were renewed.

Nothing is written to their customers either way. Their own switches are left exactly as they were, so switching the reseller back on (or *start their allowance again* after the next payment) restores precisely the service that was running, rather than reviving plans that were stopped on purpose.

## Removing one

You are asked what becomes of their customers, because both answers are expensive to get wrong:

- **Keep them, under me** — the plans stay live and every configuration people are holding keeps working. Only who administers them changes.
- **Delete them too** — what the reseller sold stops, and their addresses go back to the pools.

Either way their customers leave the reseller's group, and the group itself goes only if nothing else is filed under it — a group you share with customers of your own stays. A node that was reserved for them goes back to the shared pool rather than being deleted.

## What holds this up

The separation is not the menu. Every endpoint checks for itself, and a reseller's reads and writes of customers are narrowed to their own by the database, on a rule registered once rather than a condition written out per query — so a path nobody has thought of yet is narrowed too.

A device is reached through its customer: removing one, or reading its configuration, first asks whether its customer is one the reseller can see, and answers *not found* when it is not — the same answer an id that does not exist gets, so counting through ids learns nothing.
