# Changelog

All notable changes to W-UI are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

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

[Unreleased]: https://github.com/AbolfazlTafakori/w-ui/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/AbolfazlTafakori/w-ui/releases/tag/v1.0.0
