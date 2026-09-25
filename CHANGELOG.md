# Changelog

All notable changes to W-UI are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

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

[Unreleased]: https://github.com/AbolfazlTafakori/w-ui/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/AbolfazlTafakori/w-ui/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/AbolfazlTafakori/w-ui/releases/tag/v1.0.0
