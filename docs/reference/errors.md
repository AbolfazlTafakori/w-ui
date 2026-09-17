---
description: "Every message the panel, the installer, the update script and the w-ui menu can show, what each one means, and what to do about it."
---

# Errors

Every error W-UI shows is a sentence that names what is wrong. This page lists them all, by where you meet them, with what each means and what to do. Messages are quoted exactly; `…` stands for a value that changes (a name, a number, a path).

## Where errors appear

| Where | How |
|---|---|
| **The panel** | A red toast, or — for a field you typed — the message under that input. |
| **The API** | JSON: `{"error": "…"}`; for one input, `{"error": "…", "field": "name"}`. Status `400` bad input, `401` not signed in, `403` refused, `404` not there, `413` too large, `429` locked out, `500` the panel's own fault (details are in the log, never in the answer). |
| **The panel's log** | `journalctl -u wui`. `level=ERROR` is something that failed; `level=WARN` is something worth knowing; both carry `error=` with the underlying cause. |
| **The installer** | `error: …` in red, and the install stops. Everything before it was done; nothing after it. |
| **`update.sh`** | The same; a failed update leaves the old panel running. |
| **The `w-ui` menu** | `[ERR] …` in red. |

An error that says **internal error** is a bug or a broken server, not bad input: read `journalctl -u wui -n 50` for the line that starts with `request failed`, and report it with that line.

## Signing in and sessions

| Message | Meaning | What to do |
|---|---|---|
| `incorrect username or password` | One of them is wrong. The same answer for both, on purpose, so a name cannot be guessed on its own. | Check both. Reset with `w-ui` → 7, or `wui admin reset --username NAME`. |
| `that code is not right` / `that code is not right. Check your phone's clock is correct and try the next one.` | The two-factor code did not match. Codes are 30 seconds wide; a phone whose clock drifts produces codes the server rejects. | Wait for the next code. Set the phone's clock to automatic. Lost the authenticator: `w-ui` → 7 and answer **y** to disabling two-factor. |
| `that password or code is not right` | Turning two-factor on or off needs your current password (and code, when on). | Retype it. |
| `too many attempts from this address; try again in … ` (429) | Sign-in is throttled per address and per account after repeated failures. | Wait the time shown. If it was not you, `w-ui` → 22 shows who is knocking. |
| `your session has ended; sign in again` | No token was sent, or the token's cookie is missing — a token copied out of one browser into another does not work on its own. | Sign in again in this browser. |
| `session expired, sign in again` | The token is past its life (Settings → Security → session duration) or is not one this panel issued. | Sign in again. |
| `you were signed out everywhere; sign in again` | The password was changed or **Sign out everywhere** was used since this token was issued. | Sign in again. |
| `that access token is not valid` | A `wui_…` API token that was revoked or never existed. | Issue one on the API page, or `wui token issue --name NAME`. |
| `not signed in` | `/api/auth/me` was called without a session. | Sign in. |
| `the current password is incorrect` (403) | Changing the password needs the old one. | Retype it; forgotten: `w-ui` → 7. |
| `the new password must be at least 8 characters` | | Choose a longer one. |
| `the username can be at most 64 characters` | | Choose a shorter one. |
| `give a new username, a new password, or both` | The change form was submitted empty. | Fill in what should change. |

## Requests the panel cannot read

| Message | Meaning | What to do |
|---|---|---|
| `the request body could not be read as JSON` | The body is not valid JSON. | From a script: check quoting; send `Content-Type: application/json`. From the panel: reload the page — it is a version mismatch. |
| `the request had no body` | A POST/PUT arrived with nothing. | Send the document. |
| `that request is too large` (413) | Bodies are capped at 1 MB; uploads (backups, profiles) have their own, larger caps. | For a backup, use the upload endpoint; for anything else, the request is wrong. |
| `this request carried a field this server does not know: …. The panel and its interface are probably different versions` | The JSON has a key the server has no use for. | Reload the panel after an update; from a script, drop the field. |
| `unsupported language "…"; available: …` | `/api/i18n/xx` for a locale that is not shipped. | Use `en` or `fa`. |
| `that is not a range this panel keeps. Ask for 5m, 1h, 6h, 24h, 48h or 7d` | History was asked for a window the store does not have. | Use one of those. |
| `action must be enable, disable or delete` | A bulk action with an unknown verb. | One of the three. |
| `no file was sent` / `that file could not be read, or it is larger than this panel accepts` | An upload with no file part, or one past the cap. | Attach the file; a backup larger than the cap is not a W-UI backup. |

## Customers (clients)

| Message | Meaning | What to do |
|---|---|---|
| `name is required` | | Give the customer a name. |
| `choose at least one server for this customer` | No interface was ticked. | Tick the tunnel(s) the customer may use. |
| `Not found: …` | An interface id that does not exist — usually a stale page after a deletion. | Reload; pick an existing tunnel. |
| `one of those inbounds does not exist` / `choose at least one inbound` | The same, on bulk creation. | |
| `device limit must be between 0 and 50` | Connections at once; 0 is unlimited. | A number in that range. |
| `… devices requested; at most … per customer` | Device files are capped at 64. | |
| `expiry is in the past` | The expiry date has already passed. | Pick a future date, or leave it empty for no expiry. |
| `unknown reset cycle "…"` | | `none`, `daily`, `weekly` or `monthly`. |
| `the duration cannot be negative` | Start-on-first-use days below zero. | Zero or more. |
| `this customer already has a device called "…"` | Device names are unique per customer. | Another name. |
| `there is no group called "…"` | A rule or a bulk action named a group that does not exist. | Create the group first, or fix the name. |
| `a group called "…" already exists` / `a group needs a name` / `that name is too long` | | |
| `a tag prefix can only contain letters, digits, - and _` / `a tag cannot contain a comma or a space` | | |
| `an OpenVPN username and password only apply when the customer is on an OpenVPN tunnel` | Credentials were typed for a customer with no OpenVPN tunnel ticked. | Tick an OpenVPN tunnel, or leave the fields empty. |
| `an OpenVPN username is 3 to 48 characters` / `an OpenVPN username can only contain letters, digits, - _ . and @ (found "…")` | | Something like `roya` or `roya.k`. |
| `the username "…" is already used on …` | Usernames are unique per OpenVPN tunnel. | Another name. |
| `an OpenVPN password is 6 to 64 characters` / `an OpenVPN password cannot contain spaces` | | |
| `a subscription id is 8 to 64 characters` / `a subscription id can only contain letters, digits, - and _ (found "…")` | The subscription id typed on the Credentials tab. | Something like `roya-2024-link`, or leave it empty to have one drawn. |
| `the subscription id "…" belongs to another customer` | Each customer's link secret is unique. | Another id. |
| `device limit reached` (400) | The customer already holds 64 device files, the most one can. | Remove a device. |
| `address pool exhausted` (400) | The tunnel's subnet has no free address. | A larger subnet on the interface, or another interface. |
| `no interfaces configured; create one before adding customers` (log) | A customer was created before any tunnel. | Create an interface first. |

## Interfaces (tunnels)

| Message | Meaning | What to do |
|---|---|---|
| `a tunnel needs a name` / `name is required` | | |
| `a tunnel called "…" already exists on …` / `a tunnel called "…" already exists on this server` | Names are unique per server. | Another name. |
| `a tunnel name is at most 15 characters; "…" is …` / `a tunnel name can only contain letters, digits, - and _ (found "…"); it names a network device, not a host` | The name becomes the kernel device (`wg0`); a domain typed here would fail at bring-up. | Something like `wg0` or `ir443`; the domain goes in **Endpoint**. |
| `… overlaps …, the subnet of tunnel "…"; every tunnel on a server needs its own range` | Two tunnels on one range give the kernel two routes to the same addresses. | A different range, like `10.67.0.0/16`. |
| `"…" is too small for the … devices on this tunnel` | The new subnet cannot hold the devices already issued. | A larger range. |
| `unknown protocol "…"` | | `wireguard` or `openvpn`. |
| `no driver available for "…" on this server` | The kernel or the binary for that protocol is missing here (`wg`/`awg`/`openvpn`). | Install it: re-run the installer. |
| `listen port … is out of range` | | 1–65535. |
| `port … is already in use on this server (…).` `Something else is listening there — another VPN, or another program.` `Choose a different port` | The UDP/TCP port could not be bound. | Another port, or stop what holds it (`ss -lunp`). |
| `the panel is not allowed to bind port … (…). ` | Below 1024 without the capability. | The installer's unit grants `CAP_NET_BIND_SERVICE`; a hand-written unit needs it too. |
| `subnet "…": …` | Not a CIDR, or too small. | Like `10.9.0.0/24`. |
| `endpoint host is required` / `endpoint host is required; it is what clients dial` | The address customers connect to. | The server's public name or address. |
| `"…" needs a port, as in vpn.example.com:51820` / `"…" has no host part` / `"…" is not an address the panel can read` | A malformed endpoint or host. | |
| `MTU … is out of range (576-9000)` | | |
| `AmneziaWG mode applies to WireGuard only` / `unknown mode "…"` | | |
| `… needs AmneziaWG, and this kernel has no …` | The obfuscated mode needs the `amneziawg` module or `amneziawg-go`. | Re-run the installer (it installs both); or plain WireGuard. |
| `… already exists and belongs to another WireGuard …` / `… already exists and is not a tunnel this panel …` | A kernel interface of that name exists and is not ours. | Another name, or remove the stray interface (`ip link del`). |
| `… did not come up within …` / `interface did not start` (log) | The driver could not bring the link up. | The log line's `error=`: usually a port, a module or a permission. |
| `… still carries … interface(s); remove them first` | Deleting a node that still has tunnels. | Delete the tunnels first. |
| `… device(s) still use "…"; remove those clients first` | Deleting an interface that has customers. | Move or delete them first. |

## Hosts and host groups

| Message | Meaning | What to do |
|---|---|---|
| `a host has to belong to an interface` / `there is no interface …` | | Pick the tunnel. |
| `give the host a name so the list can be read later` / `give the host a name; it is what the config is called` | | |
| `a host needs the address customers will dial` / `"…" is not a host name or address` / `"…" is not an address` | | A hostname or IP. |
| `… is not a port number` | | 1–65535, or 0 to inherit the tunnel's. |
| `… already has a host called "…"` | | Another name. |
| `the name is longer than 256 characters` / `the description is longer than 64 characters` | | |
| `"…" is not a format; the formats are …` | Excluding a config format that does not exist. | One of the listed formats. |

## Outbounds, balancers and routing

| Message | Meaning | What to do |
|---|---|---|
| `an outbound needs a tag; routing rules refer to it by that name` | | |
| `a tag can only contain letters, digits, - and _ (found "…")` / `that tag is too long` | | |
| `"…" is the name of a built-in outbound` | `direct` and `block` are reserved. | Another tag. |
| `an outbound called "…" already exists` / `an outbound is already called "…"` | | |
| `"…" is not an outbound kind this panel serves` | | A kind the form offers. |
| `an outbound of this kind needs an address to reach` / `"…" is not a URL. It should look like https://vpn2.example.com:2096` | | |
| `a WireGuard hop needs the upstream peer's public key` / `that is not a WireGuard key` | Keys are 44-character base64. | Paste the key from the upstream's config. |
| `an OpenVPN outbound needs the client profile (.ovpn)` / `the OpenVPN profile has no remote line` | | Paste the whole profile. |
| `a … outbound needs its outbound object; paste the JSON or a share link` | | |
| `not a share link, a WireGuard configuration, an OpenVPN profile or an Xray outbound` / `nothing to import` | The import box did not recognise the text. | One of those, whole. |
| `that … link is malformed` / `that vmess link is not base64` / `that vmess link does not carry JSON` / `that ss link has no host and port` / `that is not valid JSON: …` | A share link that does not parse. | Copy it again from where it came from. |
| `"…" links are not something this panel can run` / `protocol "…" is not one this panel can run` | A scheme the panel has no engine for. | |
| `an MTU of … is outside the usable range of 576 to 1500` / `a keepalive of … seconds is outside 0 to 65535` / `"…" is not a range; it should look like 0.0.0.0/0` | | |
| `"…" does not resolve, so there is nothing to test` / `give a domain or address to test` / `"…" is not a member of …` | The test dialog. | |
| `could not bring up an outbound hop` (log) / `outbound hop process died; restarting` (log) | The hop's process failed. | `error=` names it: a bad key, an unreachable upstream, a missing binary. |
| `a balancer needs a tag; rules refer to it by that name` / `a balancer called "…" already exists` | | |
| `a balancer needs at least one outbound to send traffic to` / `there is no outbound called "…"` / `"…" has no device to balance over; a balancer's members are hops` | Members must be hop outbounds that exist. | |
| `"…" is not a strategy; use random or leastPing` | | |
| `"…" has no device; the fallback has to be a hop` | | |
| `balancer "…" still includes "…"; remove it from the balancer first` / `… routing rule(s) still send traffic to "…"; change or remove them first` | Deleting something still referenced. | Remove the references first. |
| `"…" is built in and cannot be removed` | | |
| `give the rule a comment so the list can be read later` | Rules need a name. | |
| `the rule matches nothing as written; fill in at least one criterion` | Every match field is empty. | At least one of source, destination, domain, port, client, group, interface. |
| `"…" is not a network the router matches; use tcp, udp or icmp` / `icmp has no ports; drop the ports or pick tcp or udp` | | |
| `there is no outbound or balancer called "…"` | | |
| `a client is named by id here, and "…" is not one` / `an inbound is named by id here, and "…" is not one` / `there is no inbound with id …` | Rules refer to customers and tunnels by number. | The id from the list. |
| `"…" is a balancer; the default has to be an outbound. Point a rule at the balancer instead` | | |
| `"…" has no dot in it, so it is not a domain name` / `"…" is too long to be a domain name` | | |
| `unknown domain strategy "…"` / `unknown query strategy "…"` | | One the form offers. |
| `DNS needs at least one server to forward to` / `DNS server …: port … is out of range` | | |
| `traffic routing inactive: outbounds and routing rules are stored but not applied` (log) | The routing engine could not start (needs `nftables` and `CAP_NET_ADMIN`). | Fix what the `error=` names; until then traffic goes out directly. |

## Subscription settings

| Message | Meaning | What to do |
|---|---|---|
| `"…" is already used by the panel itself` / `the path cannot start with /api/, which the panel serves` | The subscription path would shadow the panel. | Another path, like `/subscribe/`. |
| `a path can only contain letters, digits, - and _ (found "…")` / `that path is too short to be worth having` | | At least two characters. |
| `an update interval of … hours is outside the useful range of 1 to 168` | | |
| `"…" is not a template; choose one of …` | | One of the listed templates. |
| `that notice is too long` / `that title is too long` | | |
| `a certificate and its key go together` | One path without the other. | Both, or neither. |
| `"…" is not an IP address` | The listen address. | An address, or empty for all. |
| `… is not a port number` | | |
| `a subscription needs a URL to fetch` / `that is not an http or https URL` / `too many redirects` / `outbound subscription fetch failed` (log) | Outbound subscriptions. | Check the URL in a browser. |
| `subscription certificate is unusable; serving it plain` (log) | The subscription listener's certificate files could not be read. | Fix the paths or permissions (`chown wui`). |
| `subscription listener failed` (log) | Its port could not be bound. | Another port, or free it. |

## Panel settings

| Message | Meaning | What to do |
|---|---|---|
| `panel port … is out of range` | | 1–65535. |
| `listen IP "…" is not an address` | | |
| `the URI path is one segment, like /panel/` | The base path has a slash in the middle. | One segment. |
| `session duration must be between 1 and … minutes` / `page size must be between 0 and 1000` | | |
| `unknown language "…"` / `unknown time zone "…"` / `unknown calendar "…"` / `unknown log level "…"` / `unknown log format "…"` | | One the form offers. |
| `trusted proxy "…" is not an address or a CIDR` | | Like `10.0.0.5` or `10.0.0.0/8`. |
| `the collection interval is 0 to 3600 seconds` / `the online window is 10 to 86400 seconds` / `the probe interval is 10 to 86400 seconds` / `the stale TTL cannot be negative` | | |
| `expiry must be between 0 and … days` / `device limit must be between 1 and …` | Defaults for new customers. | |
| `backup interval must be between 0 and … hours` / `keep between 0 and 365 backups` | | |
| `notifications need a chat id` / `unknown bot language "…"` / `notification thresholds cannot be negative` / `a threshold is a percentage, 0 to 100` / `notification time: …` | Telegram settings. | |
| `the Telegram API server must begin with http:// or https://` / `the external traffic URI must begin with http:// or https://` / `the test URL must begin with http:// or https://` | | |
| `a mail server is required to send email` / `a from address is required to send email` / `at least one recipient is required to send email` / `mail port … is out of range` / `unknown mail encryption "…"` | | |
| `could not deliver a notification` / `telegram bot could not send` / `telegram bot could not poll` (log) | Telegram was unreachable, or the token is wrong. | `error=`: a 401 is a bad token; a timeout is the network. |

## Nodes

| Message | Meaning | What to do |
|---|---|---|
| `a node needs a name` / `a node called "…" already exists` | | |
| `a node needs an address` / `"…" is not a URL. It should look like https://vpn2.example.com:2096` | | The other panel's full URL, port included. |
| `"…" is a private address; turn on …` | A loopback or private address was refused as a node. | Use the public address, or enable private nodes in Settings. |
| `a node needs an access token. Create one on that …` / `an access token is needed` | | On the other panel: API → new token. |
| `pinning needs a certificate to pin. Fetch it from the node, or paste a sha256/… fingerprint` | | |
| `there is no server with id …` / `this panel's own entry cannot be removed` | | |
| `this node only accepts a managing panel that presents a client certificate` (401) / `that client certificate was not signed by the authority this node trusts` | The node requires mTLS and this panel did not present, or presented the wrong, certificate. | Settings → Nodes on both sides: exchange the certificate again. |
| `node became unreachable` / `node is not in step with this panel` (log) | The node stopped answering, or its data diverged. | Check the node's own panel; a sync runs on the next poll. |
| `node address is plain HTTP; its token travels unencrypted` (log) | | Give the node a certificate and use `https://`. |

## Backup and restore

| Message | Meaning | What to do |
|---|---|---|
| `"…" is not a backup file` / `"…" not found` | The name is not one of ours, or is gone. | Pick from the list. |
| `"…" is not a gzip archive` / `"…" is damaged: …` | Truncated download or wrong file. | Download it again. |
| `"…" holds no database, so it is not a W-UI backup` | The archive has neither a `wui.db` nor a portable dump. | It is not a W-UI archive. |
| `"…" contains a file far too large to be one of ours` / `"…" expands to more than this can restore` | Size guards against a crafted archive. | |
| `"…" tries to write outside the data directory` | A path traversal in the archive. | It is not a W-UI archive. |
| `the uploaded file is empty` | | |
| `could not save the current state before restoring: …` | The safety copy failed, so nothing was restored. | Disk space, or permissions on the backup directory. |
| `this backup was written by a newer panel (format …); update first` | The portable dump is newer than this panel understands. | `w-ui update`, then restore. |
| `not a portable backup: …` | `wui-export.json` is not readable. | |
| `the restored backup could not be loaded into the database; the panel is running on the data it had` (log) | The dump did not import; kept as `.restore-import.json.failed`. | The `error=` names the table and column; report it. |
| `the archive holds neither a database file for this engine nor a portable dump; only the other files are restored` (log) | An old archive restored into the other engine. | Restore it into a SQLite panel, take a new backup there, restore that. |
| `restored, but this server's own addresses could not be put back` (log) | The archive's addresses stayed. | Check each interface's endpoint. |
| `scheduled backup failed` / `could not snapshot the database; archiving the live file instead` (log) | | Disk space or permissions; the second is only a warning. |
| `snapshots are only available for sqlite` | Seen in the log on PostgreSQL; the dump is what carries the data there. | Nothing. |

## Starting the panel

These end the process; `journalctl -u wui -n 30` shows them.

| Message | Meaning | What to do |
|---|---|---|
| `another W-UI panel is already running here: …` `Two panels on one machine overwrite each other's firewall rules, and neither notices, so this one will not start` | A second copy was started. | Stop the other, or use it. Running `wui` by hand while the service runs does this. |
| `config: WUI_LISTEN must not be empty` / `config: unknown database driver "…", want sqlite or postgres` / `config: WUI_DB_SOURCE is required for driver "…"` / `config: unsupported locale "…", want en or fa` | Environment in `/etc/systemd/system/wui.service` or `/etc/wui/*.env`. | Fix the variable. |
| `config: WUI_BASE_PATH "…" must be a single path segment` / `config: WUI_BASE_PATH "…" may only contain letters, digits, and - _ . ~` / `config: WUI_BASE_PATH may not be "…"; it collides with the API` | | |
| `config: WUI_TLS_CERT is set without WUI_TLS_KEY` / `config: WUI_TLS_KEY is set without WUI_TLS_CERT` / `config: the certificate and key do not form a usable pair: …` | | Both files, matching. |
| `config: WUI_COLLECT_INTERVAL is …, minimum is 1s` | | |
| `database: open …: …` | The database could not be opened. | SQLite: permissions on `/var/lib/wui`; PostgreSQL: the server is down or `db.env` is wrong — `w-ui` → 25 → 3. |
| `database: migrate: …` | The schema could not be brought up to date. | Report it with the line; a restore of the last backup is the way back. |
| `http server: …` | The panel's port could not be bound. | Another port, or free it (`ss -ltnp`). |
| `frontend placeholder embedded; run npm run build in web/ and rebuild` (log) | A binary built without the web interface. | Use a release build. |
| `created first admin account; this password is shown once` (log) | Not an error: a fresh database made an administrator. | Note the password. |

## The enforcement engine

| Message | Meaning | What to do |
|---|---|---|
| `this kernel has no nft_quota support` / `quota enforcement inactive: limits are recorded but not applied` | The kernel lacks `nft_quota`, so data limits fall back to polling and overshoot. | A stock distribution kernel; `modprobe nft_quota`. The Overview page shows the mode. |
| `…: nft not found on PATH; install nftables` / `…: cannot read the ruleset (needs CAP_NET_ADMIN): …` | | The installer installs nftables and grants the capability; a hand-made unit needs both. |
| `…: nftables is Linux-only and this panel is running on …` | | |
| `rate limiting inactive: speed limits are recorded but not applied` / `this kernel has no HTB scheduler` | No HTB in the kernel. | A kernel with `sch_htb`; speed limits are simply not applied until then. |
| `something cleared our ruleset; rewriting it` / `the shaping hierarchy disappeared; rebuilding` (log) | Another program flushed nftables or tc. | The panel rebuilt it; find what flushes (`ufw`, another VPN panel) and stop it. |
| `enforce: reduced enforcement` | Part of the ruleset could not be applied. | The `error=` line. |

## Tunnels at run time

| Message | Meaning | What to do |
|---|---|---|
| `wgdriver: WireGuard is only available on Linux` / `ovpndriver: OpenVPN is only available on Linux` | | |
| `ovpndriver: openvpn is not installed` / `ovpndriver: /dev/net/tun is missing; load the tun module` | | Re-run the installer, or `modprobe tun`. |
| `ovpndriver: interface … has no certificates; recreate it` | The tunnel's PKI is gone (restored from an archive without it, or deleted). | Delete and recreate the tunnel; customers get new profiles. |
| `ovpndriver: the server process would not start` / `ovpndriver: the server process is not running (last pid …)` | | `journalctl -u wui` has OpenVPN's own output beside it. |
| `wgdriver: awg syncconf …` / `wgdriver: configure …` | The kernel refused the configuration. | Usually a duplicate address or an invalid key in a peer; the `error=` says which. |
| `some peers were skipped because their keys could not be parsed` (log) | A customer's account has a bad key. | Delete and re-add that device. |
| `openvpn transport changed; every customer needs their configuration again` (log) | The tunnel moved between UDP and TCP. | Hand out new profiles. |
| `this server has used its whole transfer allowance; customers are off it` (log) | The node's own allowance (Settings → Nodes) ran out. | Raise it or wait for the reset. |

## The installer

| Message | Meaning | What to do |
|---|---|---|
| `run as root (sudo bash …)` | | `sudo`. |
| `unsupported distribution: …` / `unsupported architecture: …` / `cannot read /etc/os-release; unsupported system` | Debian/Ubuntu/RHEL family on amd64 or arm64 only. | |
| `unknown option: …` | | `bash install.sh --help`. |
| `--db must be sqlite or postgres, not …` | | |
| `port … is already served by …; pass --port with a free one` | The panel port is taken by something else. | `--port N`, or let it pick one. |
| `could not install base packages` / `wireguard-tools did not install; the panel cannot serve WireGuard without it` / `nft missing; quota enforcement cannot run without nftables` | apt/dnf failed. | Check the network and the package sources; run the shown command by hand to see why. |
| `download failed` / `checksum mismatch for …: the download is not the file that was released` | | Try again; a persistent mismatch is a tampered mirror — do not install it. |
| `--from-source needs Go on PATH` / `could not install Go; use --local <path> with a prebuilt binary` / `build failed` / `could not fetch the source` / `could not unpack the source` / `the source archive does not look like this project` | Building from source. | Use a release, or `--local` with a binary you built. |
| `no such file: …` / `installed binary is not executable` | `--local` pointed at nothing usable. | |
| `could not install PostgreSQL` / `could not initialise PostgreSQL` / `PostgreSQL did not come up; see: journalctl -u postgresql` / `could not create the database role` / `could not create the database` / `could not set the database password` | The PostgreSQL step. | The named log; SQLite is one flag away (`--db sqlite`). |
| `service failed to start — see: journalctl -u wui -n 50 --no-pager` | The panel did not come up. | The log's last lines are one of the *Starting the panel* messages above. |
| `no answer — the terminal closed before the questions were finished` | Stdin ended mid-question. | Run it in a terminal, or pass `-y` and flags. |
| `cancelled — nothing was changed` / `nothing was deleted` | You said no. | |
| `cannot read randomness from /dev/urandom` | | A broken container; do not install there. |
| `port 80 is already served by …` + `no terminal to ask for another port — skipping the certificate` | Let's Encrypt needs port 80 and it is taken; unattended, the install goes on without TLS. | Afterwards: `w-ui` → 20, which asks for an alternative port. |

## `update.sh`

| Message | Meaning | What to do |
|---|---|---|
| `W-UI is not installed on this machine; run install.sh instead` | | |
| `no release … with a build for … was found` | The tag does not exist, or has no binary for this architecture. | Check the tag name; `dev-latest` for the rolling build. |
| `download failed` / `checksum mismatch for …` | | Try again. |
| `signature check failed: the download is not a build this project signed` | The binary's signature does not match the project's key. Nothing was installed. | Do not install it. If you build your own releases, sign them with your key. |
| `the installed panel cannot check signatures; the checksum is what was verified` (warning) | The panel that is installed was built without the public key. | Harmless; after this update the next one is signed-checked. |
| `the panel did not come back — see: journalctl -u wui -n 50 --no-pager` | | The *Starting the panel* messages. |

## The `w-ui` menu

| Message | Meaning | What to do |
|---|---|---|
| `Please install the panel first` / `Panel installed, Please do not reinstall` | The option needs a panel, or you already have one. | |
| `Please enter the correct number [0-28]` | | |
| `get current settings error, please check logs` | `wui setting show` failed — the database could not be opened. | `journalctl -u wui`; on PostgreSQL, is it running? |
| `panel Failed to start, Probably because it takes longer than two seconds to start, Please check the log information later` / `Panel restart failed, …` / `Panel stop failed, …` | The state did not change within two seconds. | `w-ui status` a moment later; then the log. |
| `w-ui Failed to set Autostart` / `w-ui Failed to cancel autostart` | `systemctl enable/disable` failed. | The unit file is missing: re-run the installer. |
| `Failed to download script, Please check whether the machine can connect Github` / `could not fetch the installer` | | Network to GitHub. |
| `Domain name cannot be empty. Please try again.` / `Invalid domain format: …` / `No domain given; cancelled` | | |
| `Port … is busy; cannot proceed with issuance.` / `Invalid port provided.` | The port for the ACME challenge. | Another free port; it must be reachable from the internet. |
| `Certificate issuance failed, script exiting...` / `Issuing certificate failed, please check logs` / `Failed to issue certificate for IP: …` / `IP certificate setup failed.` | Let's Encrypt refused or could not reach the server. | Port 80 (or the one you gave) open from the internet; DNS pointing here; not rate-limited (5 per week per name). acme.sh's log is in `/root/.acme.sh/`. |
| `Certificate installation failed, script exiting...` / `Installing certificate failed, exiting.` / `Certificate files not found after installation` / `Error: Certificate or private key file not found for …` | The files did not land in `/etc/wui/certs/<name>/`. | Permissions; run the option again. |
| `Failed to install acme.sh` / `Install acme failed, please check logs.` / `Installation of acme.sh failed.` / `install socat failed, please check logs` | | Network; `curl https://get.acme.sh` by hand. |
| `Auto renew failed, certificate details:` / `Auto update setup failed, script exiting...` | acme.sh's renewal or upgrade hook. | The details printed under it. |
| `Backup failed: …` / `backup failed: …` / `Restore failed: …` / `restore failed: …` | From `wui backup`; the text after the colon is the panel's own message, listed under *Backup and restore*. | |
| `The panel did not come back; see: journalctl -u … -n 50` / `the panel did not come up on …; see: …` | After a restore or a move between engines. | The log; the previous state was kept — the line printed with it says how to go back. |
| `PostgreSQL is not installed` / `PostgreSQL is not set up for the panel yet; choose 1 first` / `PostgreSQL install failed` | | Option 25 → 1 first. |
| `No such file` | The restore path does not exist. | |
| `ERROR: You must be root to run this script!` | | `sudo w-ui`. |
