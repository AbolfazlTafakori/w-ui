# Changelog

All notable changes to W-UI are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

## [2.1.2] — 2026-09-28

### Fixed
- A reseller, a server or a node created switched off came up switched on.
  The schema defaults "enabled" to true, and an insert took a false for
  nothing given: a reseller the owner meant to prepare before selling could
  sell at once, and a server meant to be configured first came up carrying
  traffic.
- A reseller's pause is decided in one place and told in one set of words.
  The three reasons were spelled out in three places, one of them in
  different words, beside two helpers nothing called.

### Documentation
- The resellers page states the rule exactly: a customer is served when both
  their own switch and their reseller's standing allow it, with a table of
  every case, the order the three reasons are checked in, what happens the
  moment a reseller is switched off, and what they can still do.
- Every message a reseller or the Resellers page can show is in the error
  reference, in English and Persian.
- The Telegram channel, [@wuipanel](https://t.me/wuipanel), is linked from the
  README and from every page of the documentation.

## [2.1.1] — 2026-09-28

### Fixed
- Switching a reseller off or on, or changing their date or allowance, now
  takes their customers off the tunnels -- or puts them back -- the moment it
  is saved, rather than on the panel's next pass. Their customers' own
  switches are still never written: a customer switched off before the pause,
  or during it, stays off when the reseller comes back.

## [2.1.0] — 2026-09-28

### Added
- The Resellers page has the Clients page's search, filters and sort: search
  by username, note or group, filter by status and server, and sort by name,
  customers, traffic used, time left or newest. The summary tiles filter too,
  and the view is kept in the address.
- A customer whose reseller is paused reads *Reseller paused* on the Clients
  page (*Account paused* to the reseller), and their own subscription page
  says they are off. They read "Active" while being unable to connect.

### Fixed
- A tick that could not read which resellers are paused treated every one as
  in good standing, putting a switched-off reseller's customers back on the
  tunnels until the next good read. It now keeps the last answer.
- On a phone, a time-left tag in Persian ran its number and its words in the
  wrong order, and a long one pushed out of its card.
- On a switched-off reseller's row the switch and the actions faded with the
  rest, and read as buttons that could not be pressed.
- Sixteen Persian labels were still in English, among them "On hold" and the
  client dialog's tabs.

## [2.0.0] — 2026-09-28

The panel can be sold on. A second kind of operator signs in beside the
owner — a reseller who manages only their own customers, within a ceiling the
owner sets — and the whole panel has had a security and accessibility pass.

Upgrading keeps everything as it was: the account that administers the panel
today becomes its owner, and the database takes its new tables on first start.

### Added
- **Resellers.** A reseller signs in with their own username and password and
  manages only the customers they create, on the servers the owner gives them,
  within a ceiling of customers, traffic and time. They never reach the machine:
  no interfaces, nodes, routing, settings or backups, and no sight of anyone
  else's customers. The separation is enforced by the database on every query,
  not by the menu.
- A **panel administrator** role: every customer on the panel, no ceiling, and
  nothing about the server itself.
- **Resellers** is a page in the main menu, laid out as the customer list is: a
  summary across the top, and a row per reseller with customers, traffic and
  time left in the customer list's colours. On a phone each is a card.
- The reseller dialog is the customer dialog's shape and fits one screen. The
  username and password are drawn for you, side by side with a button to draw
  again, and one copy button puts the address, username and password on the
  clipboard together. Presets fill a typical reseller plan.
- A reseller's term is picked from a calendar — Gregorian or Jalali, as the
  panel is set — with +1 month, +3 months and +1 year counted from the date
  already set, or put **on hold**: a number of days that starts at their first
  sign-in.
- The owner files each reseller's customers under a group of their choosing,
  which the reseller never sees; changing it moves the customers already there.
- A reseller's own customer list opens with **Your account**: customers against
  their limit, traffic left and time left, and the reason when it is paused.
- Switching a reseller off stops every customer they hold and signs them out;
  a term that ends or traffic that runs out stops them the same way, and they
  may still sign in, read-only, to see why. Nothing is written to their
  customers, so switching them back on restores exactly what was running.
- Removing a reseller asks whether to keep their customers under the owner or
  delete them too.

### Security
- Changing your own password and turning off the second factor are throttled
  as sign-in is. With a borrowed session either could be used to guess the
  password, or a six-digit code, as fast as requests could be sent.
- A new username chosen on the security page follows the same rules as a new
  operator's, case-insensitively, so nobody can become "Admin" beside "admin".
- A subscription link chosen by hand is unique across the whole panel, and
  asking for taken ones over and over is stopped, so it cannot be used to find
  out which links exist.
- Customer and device names and notes are held to their columns and to one
  line. A longer one was a database error on PostgreSQL; a line break in one
  reached the log, the export and Telegram messages.
- After signing in, the panel only follows a `next` address on itself.
- A device is removed, and its configuration read, only by an operator who can
  see its customer.

### Accessibility
- Secondary text meets 4.5:1 in both themes; it was 3.6:1 on every page. Red
  text uses a lighter red on dark surfaces, and filled danger buttons a darker
  one, so white on them reads.
- Gold server chips were unreadable in the light theme (1.9:1), and the upload
  and download figures failed in one theme or the other.
- Tabbing through a table of row actions showed no focus at all: the buttons'
  own shadow cancelled the focus ring.
- Every page names itself in the browser tab and carries a heading for a
  screen reader.
- The × buttons on alerts, search boxes and chips take a tap 24px across
  without looking any larger, and chips and small links are 24px tall.
- The settings selects are named, the suggestions under a free-text field
  close when focus leaves by keyboard, and its clear button says what it does.

### Fixed
- An OpenVPN server carried across a panel upgrade no longer refuses every
  customer. It kept the mount namespace of the panel that started it, so once
  the unit's writable paths changed it saw its own directory as read-only and
  could not write the file OpenVPN makes for each login — every attempt came
  back as "user authentication failed". The panel now checks that a server it
  adopts can still write there, and restarts it when it cannot.
- A tunnel whose engine died is brought back up. Its driver was kept as long as
  the interface's settings were unchanged, so the panel went on shaping and
  billing an interface nobody could reach; an open tunnel that reports itself
  unhealthy is reopened on the next tick.
- The dropdown arrow on free-text pickers had no icon.

## [1.1.0] — 2026-09-25

### Added
- **New keys.** A customer can be issued fresh credentials: a new WireGuard
  key pair and preshared key, and a new OpenVPN password, so a file that
  leaked stops working within seconds. The key on their row does everything
  at once — every file and the subscription link together. The Credentials
  tab replaces one user's file of one kind on its own, leaving their other
  files, the other users and the link alone. `POST /api/clients/{id}/rotate-keys`
  takes `user`, `protocol`, `accountIds` and `subToken`.
- **Usage by user and tunnel** on the subscription page: a row per user, a
  column per tunnel, a total at each edge, for people sharing a plan and
  its cost.
- A customer can be in **several groups**, picked or typed on the client
  dialog and shown as chips on the list; the groups page adds and removes
  members one group at a time.
- **Add Bulk**, the classic dialog: a quantity, a naming method — random,
  prefix and number, or prefix, random and postfix — with a live example,
  and the plan they all share.
- Persian is set in **Vazirmatn**, bundled with the panel and carried in the
  binary for the subscription page.

### Changed
- **The subscription service on a port of its own is now the only port that
  answers.** The panel's port stops serving subscriptions, as the classic
  panel's does, and the link names the service's port rather than the one
  the panel was reached on. Links that named the panel's port must be
  reissued to customers.
- **Listen Domain**, when set, is the only host the subscription service
  answers to; any other name gets a 404.
- Each tunnel is charged with what crossed it: every file keeps its own
  counters and the interfaces page sums a tunnel's files, so a customer on
  WireGuard and OpenVPN no longer puts one allowance on both.
- The QR dialog is a tab per tunnel over one code rather than a panel per
  file, so a plan for seventeen users is still one screen.
- Every dialog is at most one screen tall and scrolls inside, with no
  scrollbar drawn over its controls; floating menus stay on the screen.
- Fail2ban is set up in seconds rather than minutes at install.
- Byte figures carry two decimals from a gigabyte up.

### Fixed
- The per-tunnel figures add up to the customer's total. They were summed
  from the tunnels' own counters, which include WireGuard's encryption
  overhead and ran a few percent above what the kernel charges.
- An OpenVPN password that changes now ends the session it was logged in
  with, rather than leaving the old one good until the customer reconnects.
- Behind a relay, a port is a flow and not a device, so one phone
  reconnecting is no longer reported as sharing.
- On the subscription page the language menu drops down again and the live
  badge shows its dot and word instead of a grey block.
- The group picker offers a group made empty on the groups page.
- The actions column fits the key that was added to it.

## [1.0.0] — 2026-09-14

The first public release.

### Added
- WireGuard, AmneziaWG and OpenVPN interfaces on one server, with kernel
  quota enforcement, expiry, device limits and per-customer rate limits.
- Customers, groups, hosts and host groups, subscription links with 21
  page templates, QR codes, config downloads for every client app. Quotas
  in MB, GB or TB and validity in hours, days or months.
- SQLite or PostgreSQL, chosen at install; backups carry a portable dump
  beside the SQLite snapshot, so an archive from either engine or an older
  panel restores into any install. `wui backup create|list|restore`.
- The installer picks a random port for the subscription service, turns it
  on with the panel's certificate, and prints it with the rest.
- Outbounds with policy routing, balancers, fail-closed default outbound,
  DNS proxy with pins and upstream routing, engine settings.
- Telegram notifications and an interactive Telegram bot for the operator
  and for customers.
- Backups on a schedule, sharing detection, an API with tokens and docs.
- An installer that gets a Let's Encrypt certificate for a domain or for the
  server's own address, renews it unattended, and a `w-ui` management menu.
- English and Persian, with dark, ultra-dark and light themes.

[Unreleased]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.1.2...HEAD
[2.1.2]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.1.1...v2.1.2
[2.1.1]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.1.0...v2.1.1
[2.1.0]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.0.0...v2.1.0
[2.0.0]: https://github.com/AbolfazlTafakori/w-ui/compare/v1.1.0...v2.0.0
[1.1.0]: https://github.com/AbolfazlTafakori/w-ui/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/AbolfazlTafakori/w-ui/releases/tag/v1.0.0
