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

- **Users** — how many users they have sold against their limit.
- **Traffic** and **Traffic left** — what their customers have used, and what is left of the allowance.
- **Time left** — days until their term ends, or *on hold* while it has not started.
- **Servers** — the ones they may sell.

Above the list, the Clients page's controls: search by username, note or group; filter by status (active, on hold, ending soon, stopped) and by server; sort by name, most users, most traffic used, ending first or newest. The summary tiles filter too — press *Ending soon* to see who to renew. The view is kept in the address, so a reload or a shared link opens on it.

On a phone each reseller is a card with the same figures.

## Adding a reseller

The dialog is laid out as the customer dialog is.

- **Username and password** are drawn for you; the button beside each draws another. The copy button puts the panel's address, the username and the password on the clipboard together, ready to send.
- **Users.** How many users they may sell in all. A customer counts for the users their plan is for: a limit of 100 is a hundred one-user customers, or twenty-five customers for four users each, or any mix that adds up to 100. Raising a customer's users counts against it too, and lowering them frees what they held. With a limit, a reseller cannot sell a customer any number of users at once. Empty is no limit.
- **Traffic.** What their customers may carry *between them* — measured in what was actually used, not what was promised, so ten unlimited plans nobody connects to cost nothing. Empty is no limit.
- **Until.** The last day their account works, picked from a calendar in the calendar the panel is set to (Gregorian, or Jalali — Settings → General → Calendar Type). *+1 month*, *+3 months* and *+1 year* count from the date already set, so extending is one click.
- **On hold.** The term is a number of days that starts the first time they sign in, not today: sold on Friday and first signed in on Monday, they still get every day.
- **Group.** What their customers are filed under on your own customer list, so you can see whose they are. It defaults to `reseller:<username>`; type a name to use another, or an existing group of yours. They never see it. Two resellers cannot share one — the list could no longer tell them apart.
- **Servers.** The tunnels they may put a customer on. Their own customer form offers exactly these: nothing more, and nothing missing. Choose nothing and they cannot sell at all. A machine reserved for one reseller (Nodes → owner) carries nobody else.

The presets fill a typical reseller plan in one click.

Changing the group later moves all their existing customers to the new name.

## What the reseller sees

Their Clients page opens with **Your account**: users against their limit, traffic left, time left. If their term has ended or their allowance is used up it says so, and why.

When you take a server back, the customers already on it keep working, and the reseller can still rename, extend or take them off it. They cannot put anyone new there.

## Switching one off

### The rule

A customer is served when **two switches** both allow it: **their own**, and **their reseller's standing**.

- Switching a reseller off takes every one of their customers off at once.
- Switching them back on returns each customer to what **their own** switch says.
- Nothing is ever written to the customers by any of this. That is what makes the second point true: had the pause switched each customer off, switching the reseller back on could no longer tell who had been off before — and customers you had stopped on purpose would come back.

| The customer's own switch | Reseller in good standing | Reseller paused | After the reseller is back |
|---|---|---|---|
| On | served | **off** | served |
| Off (switched off before the pause) | off | off | **off** |
| Switched off *during* the pause | — | off | **off** |
| Switched on *during* the pause | — | **off** | served |
| Expired or out of traffic | off | off | off |

Your own customers, and other resellers' customers, are never affected.

### What pauses a reseller

Three things, checked in this order:

1. **Switched off** by you.
2. **Term ended** — the date passed (a term *on hold* has not started, and is not ended).
3. **Traffic used up** — their customers between them used the whole allowance.

A reseller is back only when **none** of the three applies: switching on a reseller whose term has also ended keeps their customers off until the date is extended.

### What happens, and when

The moment you save the change:

- their customers' traffic is **dropped by the kernel**,
- their devices are **taken off the tunnels** (WireGuard peers removed),
- **connected OpenVPN sessions are cut**,
- on the Clients page each of them reads **Paused** and their switch shows off and cannot be flipped; the tag's tooltip says why, and whether their own switch is on — that is, whether they come back with the reseller,
- their subscription page says they are **inactive**, without saying why.

The same happens by itself when the date passes or the traffic runs out. If the panel cannot read the resellers' standing for a moment — a database hiccup — it keeps the last answer rather than letting paused customers back on.

### What the reseller can do meanwhile

| | Can sign in | Can see their customers | Can change anything |
|---|---|---|---|
| Switched off | **no** — told *your account has been switched off*, and signed out of open sessions | — | — |
| Term ended / traffic used up | yes, to see why and pay | yes | **no** — every change is refused with the reason |

"Every change" includes the ones that would store value up for when they are renewed: new customers, extending, raising a quota, resetting traffic, bulk and group actions, imports, new devices and moving customers between servers.

## Many at once

Tick the box on each reseller, or the one in the header for everything the filters show. The toolbar then reads *n selected*, with **more** and **Delete**:

| Action | What it does |
|---|---|
| Switch on / Switch off | Switching off asks first and says how many customers go off. It signs the resellers out, exactly as switching one off does. |
| Extend term | Adds days to each running term. A term that has ended starts again from today; a term on hold gets the days added to the hold; a reseller with no end date keeps none. |
| Start their allowance again | Each reseller's traffic from zero. Their customers' own usage is not touched. |
| Set traffic allowance / Set user limit | The same value for all of them. Empty is no limit. |
| Add servers / Remove servers | Added to, or taken from, what each already has. Customers on a server taken away keep working. |
| Delete | Asks, as for one reseller, whether to keep their customers under you or delete them too. |

Every reseller in a selection goes through the same checks as when changed alone. One that cannot be changed does not stop the rest: the answer says how many changed, and names each that did not with the reason — the owner's own account is never part of a selection, and a panel administrator has no ceiling to extend or set. Only what the filters show can be selected, and narrowing the list drops whatever it hides.

## Removing one

You are asked what becomes of their customers, because both answers are expensive to get wrong:

- **Keep them, under me** — the plans stay live and every configuration people are holding keeps working. Only who administers them changes.
- **Delete them too** — what the reseller sold stops, and their addresses go back to the pools.

Either way their customers leave the reseller's group, and the group itself goes only if nothing else is filed under it — a group you share with customers of your own stays. A node that was reserved for them goes back to the shared pool rather than being deleted.

## What holds this up

The separation is not the menu. Every endpoint checks for itself, and a reseller's reads and writes of customers are narrowed to their own by the database, on a rule registered once rather than a condition written out per query — so a path nobody has thought of yet is narrowed too.

A device is reached through its customer: removing one, or reading its configuration, first asks whether its customer is one the reseller can see, and answers *not found* when it is not — the same answer an id that does not exist gets, so counting through ids learns nothing.
