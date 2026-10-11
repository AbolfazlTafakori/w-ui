---
description: "The page a customer sees when they open their subscription link in a browser. It is server-rendered, so it works without JavaScript and loads fast on a bad connection."
---

# Subscription page

The page a customer sees when they open their subscription link in a browser. It is server-rendered, so it works without JavaScript and loads fast on a bad connection.

## What is on it

- how much of the plan is used, how much is left, when it ends, when they were last online;
- **Usage by user and tunnel** — a fold-out table: one row per user, one column per tunnel, what each spent on each, and a total at the end of every row and under every column. Every figure is counted by the kernel on that device's own tunnel address, so the rows add up to the usage above to the byte. A plan shared by several people, and paid for together, is settled from it. For a plan whose current period began before 2.6.6, the table counts from the update and says so (**Split by user since …**): what came before was an estimate and was not kept;
- one block per device **per host** — the config text, a copy button, a download, and a QR code that opens centred over the page;
- for an L2TP or PPTP user, the row is its setup: the customer opens the device list -- its search is at the top, in English or Persian, by name, brand or model -- picks their device or router, and the page shows that device's own fields filled in with the user's sign-in, which is shown there and nowhere else. A second connection to choose (L2TP without IPsec) appears only where the tunnel has one. WireGuard and OpenVPN files have the same card of their own, below them;
- the subscription link itself as a QR, for apps that speak subscriptions;
- a language switch (English / Persian) and a theme switch;
- the support link, the announcement and the profile title from Settings → Subscription.

## Templates

Settings → Subscription → **Template** picks one of twenty-one looks. **Preview** opens a sample page in the chosen template through a one-time link, so you see it before customers do.

| Template | Look |
| --- | --- |
| `classic` | the classic panel layout |
| `aurora` | red, blue and teal light drifting over dark navy |
| `waves` | slow gradient waves under a glass card |
| `network` | an animated canvas of connected points |
| `minimal` | white, flat, no decoration |
| `midnight` | pure black with a red glow |
| `ember` | charcoal with a warm fire glowing up from below |
| `ocean` | deep water, a slow cyan surface, a glass card |
| `forest` | dark moss with emerald light through the canopy |
| `sunset` | violet to amber across the sky, a sun on the horizon |
| `neon` | black with magenta and cyan edges that glow |
| `glass` | frosted white over a colourful blur (light) |
| `paper` | warm cream, serif headings, ruled lines (light) |
| `terminal` | green phosphor on black, scanlines, a prompt |
| `carbon` | woven carbon under brushed steel |
| `royal` | navy and gold, a gilded card edge |
| `sakura` | white and blossom pink, petals drifting down (light) |
| `frost` | ice blue and white (light) |
| `graphite` | flat grey surfaces, one blue, big radii |
| `mesh` | a slow multicolour mesh behind dark glass |
| `retro` | synthwave: a striped sun and a grid running to the horizon |

The page refreshes itself every few seconds while it is open: usage, remaining data, the bar and the rings, downloaded/uploaded, last online, and the dot in the header — **Online** while a device has a fresh handshake, **Idle** when the plan is active but nothing is connected, **Off** when the plan is not active.

Every look keeps the same content and the same controls — the theme toggle, the language, the download and copy buttons — only the dress changes.

## L2TP and PPTP sign-ins

An L2TP or PPTP sign-in is typed into the device, so it has to be on the page; it is also all a stranger needs to use the customer's plan. So the page never carries the **username, password or pre-shared key** in what it sends: each is shown as `••••••••`, and its copy button fetches the value when it is pressed and puts it straight on the clipboard, without ever writing it into the page. The same goes for a router command that has the sign-in in it (MikroTik's): it is shown with the mask and copied whole.

What that stops:

- **Anything that reads the page** -- a script or bot fetching the link, a crawler, a link preview, a saved copy, an extension that copies or summarises pages -- finds the mask. The page is also sent `no-store`, `no-referrer` and `noindex`.
- **Another site** cannot ask for a value: the request has to be a POST from the page's own origin (checked by the browser's `Sec-Fetch-Site` and `Origin`), carry a header no other site can send without a preflight the panel never answers, and carry a mark the page was issued that expires after 30 minutes (reload the page after that).
- **Drawing the values out** one request after another: 30 a minute per link and 60 per address, and every value handed over is written to the log with the customer, the device, which value and the address.
- **The link itself**: what an app, a script or a download gets from the link (`?view=raw`, `?format=zip`, the one-file download) has the same text with the sign-in masked, and says to open the link in a browser.

What no page can stop, said plainly: a browser extension the customer has allowed to *read and change* the site runs inside the page and sees what the page sees the moment it sees it; and what is on the clipboard, any program on the device can read. The protection is that nothing is there until the customer presses copy -- not a promise about what happens on a device the customer has handed to something else.

A WireGuard or OpenVPN file is its own sign-in (its key, or the login inside it), and is shown and offered as a QR for its app as before.

## The link

`https://host:port/subscribe/TOKEN` — the path is *Settings → Subscription → URI Path*. The token is the customer's **Subscription ID**: change it in the client form, or replace it with **New keys and link** on the customer's row, and the old link stops working at once. When the subscription service sits on its own port, the link carries that port and scheme — and **only that port answers**: the panel's port stops serving subscriptions, as the classic panel's does, so a customer's app never touches the panel's listener. **Listen Domain**, when set, is the only host the service answers to (any other name gets a 404) and the host the link is built with; leave it blank to answer on every name and IP. **Reverse Proxy URI** overrides both for the link when the service is reached through a proxy.

## Always current

Nothing is stored for the link: every fetch renders the configurations from the records at that moment. Whatever changed in the panel — an endpoint or port, the DNS, a host added, a device renamed, an OpenVPN username or password, a customer put on another server — is in the next fetch, and the answer is sent with `Cache-Control: no-store` so nothing in between keeps an old copy. The customer's page keeps itself current while it is open: it holds a stream to the panel, and the instant anything is changed on the panel — through the API, or by the reconciler ending or starting a plan — the panel writes into that stream and the page asks for its figures and a fingerprint of the files it was made from; the moment the fingerprint differs it loads itself again. (Every three seconds it asks on its own as well, for a network that will not hold a stream open.) So — a device renamed, an address moved, a server added or a username changed is in front of the customer within seconds, with no tap. Usage and the online dot move without a reload; an app refetches on the interval it is told (`Profile-Update-Interval`, **1 hour** by default, set under Settings → Subscription), so a change reaches every app within the hour without anyone being sent a new file.

## Formats

The same token serves apps too: `?format=conf` for a WireGuard file, `base64`, `zip` for every device at once, and the per-device, per-host download the page links to.
