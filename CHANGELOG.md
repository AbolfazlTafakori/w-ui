# Changelog

All notable changes to W-UI are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

## [2.6.1] — 2026-10-01

### Fixed
- **A panel can manage its nodes again.** Since v2.0.0 a node refused the
  token its managing panel holds for it -- "this part of the panel is the
  owner's" -- on everything the panel does through it: checking the node is
  up, sending it tunnels and customers, collecting its usage, and asking it
  to update. A node on v2.0.0 to v2.6.0 showed as unreachable, customers
  added on the panel never reached it, and its traffic was not counted.
  This applies to every API token, not only the one a managing panel holds
  for its node: as before v2.0.0, a token can again manage interfaces and
  tunnels, routing and outbounds, the engine, hosts, the subscription service
  and nodes, ask a node or this panel to update, and send the Telegram and
  mail test messages. Still refused to any token, as in v2.6.0: changing the
  panel's settings (`PUT /api/settings`; `GET` answers a token only the
  display preferences, as it does a reseller, and never the bot token or the
  mail password), backups, tokens, operators, adding, changing or removing a
  node, the subscription page template, restarting the panel, and the
  owner's own account and two-factor.

### Changed
- **A release is published only from a commit CI passed.** The release
  workflow now waits for CI on `main` to finish for the tagged commit -- up
  to 30 minutes -- and builds nothing if it finished red, never ran, or is
  still going by then. It also refuses to publish a release without the
  signing key, where it used to publish one unsigned with a warning: an
  unsigned release is one no panel installs from its update button.

### Updating
- **Update every node by hand, once.** A node on v2.0.0 to v2.6.0 refuses the
  panel's request to update itself too, so the panel's **Update** button
  cannot reach it. On each node server run:

  ```bash
  w-ui update
  ```

  From v2.6.1 on, nodes update from the panel again. The managing panel
  itself updates as usual; the order does not matter.

## [2.6.0] — 2026-09-30

### Added
- **Automatic backup to Telegram, on a time of its own.** Settings →
  Telegram → Backup: switch it on and choose every day at a time, every week
  on a day at a time, every few hours, or a crontab line. At that time the
  bot sends the database backup to the admin chat as a file, captioned with
  the panel's version, the server's name, when it was taken and its size.
  Times are in the panel's time zone.

### Fixed
- **The automatic backup no longer depends on the report.** It went only
  with the periodic report, so it came when the report did, and a report
  that could not be built took the backup with it.
- **"Every N hours" survives restarts.** The schedule counted from when the
  panel started, so a panel restarted (or updated) more often than the
  interval never sent a backup. Where it counts from is now kept in the
  database. A backup that fell due while the panel was down is sent once,
  as soon as it is back; switching it on or changing the time does not send
  one at once.
- **A failed backup is said once, not every minute**, and an archive too
  large for Telegram (past 50 MB) is no longer left open on the server.
- **How many backups to keep applies with scheduled backups off.** The
  archives the bot sends are kept on the server too, and were pruned by the
  number the panel started with instead of the one on the settings page.
- **A customer named with an underscore no longer stops the report.** The
  report is sent as Markdown, and Telegram refused the whole message.
- A backup time more often than every 10 minutes, or one that never comes
  round (the 30th of February), is refused on save.

Panels that had *Database Backup* switched on keep getting it at the time
their report went.

## [2.5.2] — 2026-09-30

### Fixed
- **Speed shows for every customer moving traffic, whatever the collection
  interval.** A customer's speed was averaged over a fixed six seconds, but
  how often traffic is read is a setting (the Engine page): read every ten
  seconds or more, the window was empty most of the time, and only customers
  asked about just after a collection had a speed -- some connected
  customers showed one, some did not. The window is now never less than
  three collections. A node's customers are held to the node's own report
  interval in the same way.
- **A speed no longer stays on screen after the customer stops.** The list
  refreshes its rows by copying the fields that arrive, and a customer with
  no speed was sent without one, so the last figure stayed; the speed is
  now always sent, null while there is none.

A customer who is connected but moving nothing -- a phone with its screen
off, holding the tunnel open -- counts as online and shows `—` for speed, as
the classic panel does.

## [2.5.1] — 2026-09-30

### Fixed
- **The Speed column shows each customer's speed.** It read "—" for
  customers moving traffic: the speed was worked out in the page from two
  readings of the stored total, inside the render, so every re-render took a
  fresh reading and saw no change -- and the stored total moves in steps a
  flush apart in any case. The server now measures it from the kernel's own
  counters every tick, averaged over the last few seconds, and the column
  shows it each way, as the classic panel does: `↑ 150 KB/s / ↓ 2.40 MB/s`
  in blue, `—` in grey while the customer moves nothing. On a phone it
  shows on the card only while they are. A customer on another node shows
  their node's last report, every 20 seconds.

## [2.5.0] — 2026-09-30

### Added
- **Time and traffic for many customers at once.** *more -> Time* and
  *more -> Traffic* add to, or take back from, every customer selected:
  months, days and hours (a month is 30 days) or TB, GB and MB (in 1024s,
  as the panel shows sizes). Before anything is applied the dialog shows
  what will happen, worked out by the panel for exactly this selection:
  how many change, come back on, stop, and are left alone and why. The
  rules: customers with no end date or no traffic limit are left as they
  are; time moves each customer's own end date -- one that ended five days
  ago and is given two is still ended, one that ended a day ago runs one
  more day; traffic moves the allowance and keeps the usage, and brings a
  customer who had run out back on; customers switched off get it and stay
  off; plans that start on first connection and have not started are left
  alone unless asked for, and then grow to the hour and still start on
  first connection; taking traffic back stops at what the customer has
  used. Taking back from five or more asks for the count to be typed.
  Nothing happens with nothing selected. `POST /api/clients/extend`, with
  `dryRun` for the preview.
- **Select every customer, across the pages.** Ticking the header offers
  *Select all N customers* -- every customer, or every one the filter
  matches (a group, a status, a search) -- and every bulk action then
  reaches all of them. Changing the filter clears it.
  `GET /api/clients/ids`.

### Changed
- *Adjust* sets a traffic limit and the renewal; adding time moved to
  *Time*, which leaves customers with no end date unlimited and plans not
  started waiting -- the old field gave both a date.
- The toolbar's *more* button has a name on phones, where its text is
  hidden, for screen readers.

## [2.4.1] — 2026-09-29

### Fixed
- **Another program wiping the firewall no longer switches enforcement off
  in silence.** The panel skipped rewriting its rules when nothing had
  changed, assuming they were still there. A firewall reload that begins
  with `flush ruleset` -- Debian's default `/etc/nftables.conf` does, so a
  plain `systemctl restart nftables` was enough -- or another VPN resetting
  the routing rules took them away: every limit, every switched-off
  customer and every route out of the tunnels stopped working while the
  panel went on as if nothing had happened. It now checks its own tables
  and routing rules before skipping, and puts them back within two
  seconds, saying so in the log. Checked on every distribution in CI by
  flushing the whole ruleset under a running panel.
- **A tunnel is refused a range already on the server.** A subnet that
  overlaps Docker's bridge, another VPN's interface or the server's own
  network was accepted and took that traffic into the tunnel; the refusal
  now names the device.
- **Cleaning up routing rules touches only the panel's own.** A rule was
  removed -- and its table flushed -- when its mark was in the panel's
  range, whatever table it pointed at; now both have to be the panel's.
- **PostgreSQL on a server that already uses it.** On RHEL-family systems
  the installer rewrote the loopback lines of `pg_hba.conf` for every
  database to scram-sha-256, which locks out any other project whose role
  keeps an md5 password. It now adds two lines for its own database and
  role, first, and changes nothing else.
- **acme.sh's default authority is no longer changed.** The installer and
  the menu set Let's Encrypt as the default for every certificate acme.sh
  keeps on the server, moving other projects' renewals; Let's Encrypt is
  now named on the panel's own requests only.
- **Deletes that an empty value could widen are guarded.** Clean-ups after
  a failed certificate request, and the uninstall, would have removed the
  whole acme.sh directory -- other projects' certificates with it -- or a
  whole system directory had a name come back empty; they now stop
  instead. The three shell scripts have no ShellCheck warnings left.
- **The panel opens faster.** Every page was one 824 kB script loaded
  before anything showed; pages are now fetched when first opened, and the
  first load is 202 kB. A tab left open across an update, asking for a
  page the new build does not have, gets a 404 -- it used to get the app
  shell with a 200 -- and loads the page afresh, once. The build warning
  about the bundle's size is gone.
- The code has no warnings from `go vet` or staticcheck on Linux or on
  Windows, where a development copy runs; the parsers staticcheck saw as
  unused there have tests.

### Added
- The docs have a section on sharing a server with other projects: what the
  panel touches, what it leaves alone, and how it recovers from what others
  do.

## [2.4.0] — 2026-09-29

### Added
- **An automatic install.** The installer's first question is now how to
  install: *1) Manual* -- every question, exactly as before and in the same
  order -- or *2) Automatic*, the default. The automatic install asks three
  things: the database, a domain (checked at once against this server's
  address, and blank if there is none), and, without a domain, whether to
  get a free certificate for the server's IP address. Everything else is
  chosen the way pressing enter through the manual questions would choose
  it -- a random free port, a random URL path, a generated administrator
  and password, a free subscription port, OpenVPN and AmneziaWG -- and the
  closing summary prints the address, the username, the password, the
  ports and the path. Nothing after the three questions asks anything: a
  certificate that cannot be issued is skipped, with port 80 left alone
  when another project on the server holds it, and can be had later from
  the menu. `--auto` and `--manual` choose without the question.

## [2.3.6] — 2026-09-29

### Changed
- **The customer list opens oldest first**, in the order customers were
  added, so a customer's place in it does not move each time another is
  added. Newest first and the other orders are a choice away as before.

## [2.3.5] — 2026-09-29

### Fixed
- **The security warnings no longer repeat themselves.** The account checks
  ran over every account and each added the same unnamed line: an owner with
  resellers saw *Two-factor authentication is off* once per account, and a
  reseller who had not signed in yet was reported as *The generated password
  has never been changed* -- a password the installer never generated. They
  are now about the owner's account, each said once. Panel administrators
  without a second factor are named together in one line; resellers are not
  warned about, their second factor being theirs to turn on.

## [2.3.4] — 2026-09-29

### Fixed
- **A restore could lock you out of the panel.** The panel's own port, URL
  path, domain and certificate files are kept in the database and laid over
  the service's settings at every start, and a restore brought the
  archive's back. An archive from another server -- or from this one before
  its port or path was changed -- put the panel on a port the firewall had
  never opened, under a path nobody had, or, when the certificate path did
  not exist on this machine, stopped it starting at all: the service then
  restarted it into the same failure forever. How this panel is reached --
  port, path, domain, certificates, trusted proxies and the subscription
  service's own listener -- is now always this server's, whatever the
  archive says; the customers, keys and accounts come from the archive.
  Reproduced on a real server layout with 2.3.3 (the panel did not
  come back) and checked with this one (it did).
- **A failed database snapshot no longer makes a backup that restores old
  data.** When SQLite's consistent copy could not be taken, the live file
  was archived instead -- one that can lack the changes still in its
  write-ahead log -- and a restore takes that file over the portable dump.
  The dump now carries the database in that case, and a backup with no
  copy of the database at all is not written: the failure is reported,
  including in the Telegram report, instead of a file that a restore would
  refuse.
- **Two backups in the same second no longer overwrite each other.** A
  restore's safety copy taken in the same second as the backup being
  restored replaced it with the state being thrown away.
- **Backups leave out an update waiting for the update helper**, which,
  restored on another machine, would have set its helper off.
- **Settings -> Backups restores and Restart panel follow the panel** through
  its restart and reload once it is back, instead of after a guessed six
  seconds -- which landed on a closed port, or on the old process just
  before it ended.
- **Backups too large for Telegram** (over 50 MB) are not sent into a bare
  413 any more: the chat is told the size and to download it from the
  panel, from both the report and the bot's backup button.
- **The guide to moving a panel to another server** said to unpack the
  archive with `tar -C /`, which puts the database where the panel never
  looks and skips every check a restore makes; it now uses the restore, and
  no longer asks for the same port and path on the new server.
- The restore confirmation says what comes from the archive -- including
  the accounts you sign in with afterwards -- and what stays this server's.
- The API reference's example for creating a WireGuard interface carried a
  transport, which WireGuard refuses.

## [2.3.3] — 2026-09-29

### Fixed
- The continuous-integration checks pass again: a declaration in the update
  download was written the way staticcheck asks, which had stopped the run
  before the race-detector tests, the PostgreSQL tests, the vulnerability
  check and the clean installs. All of them pass, the new update helper
  included on every distribution the installer supports. Nothing changes in
  what the panel does.

## [2.3.2] — 2026-09-29

### Fixed
- **Updating from the panel works on a real server.** Pressing *Install*
  answered *internal error* on every server set up by the installer: the
  panel runs as an unprivileged user, in a sandbox where its own binary is
  read-only, so it could not replace itself -- and the reason was hidden
  behind a generic message. The panel now does the half it can: it
  downloads the release and checks its signature into its own data
  directory. A small root helper the installer sets up, `wui-update.path`,
  sees the request and runs the installed binary as root with the new
  `wui apply-update`, which checks everything again with that binary's own
  key, refuses anything that is not a newer signed release, puts it in
  place and restarts the panel. Root only ever runs a build this project
  signed, and nothing in the panel's directory is trusted beyond its
  bytes: every file operation stays inside it and follows no link out of
  it. Rehearsed end to end as a real server runs it -- the panel as its own
  user, the binary root's -- from the button to the page reloading on the
  new version.
- **A server installed before this release has no helper yet**, and the
  update dialog now says so, with the one command to run as root (the
  update script), instead of a button that fails. After that, updates
  install from the panel.

## [2.3.1] — 2026-09-29

### Fixed
- **Updating from the panel no longer reports a failure that did not
  happen.** The release is tens of megabytes, and a server whose way to
  GitHub is slow took longer to fetch it than the panel lets one request
  run: the update installed, and the page said it had failed. The install
  now runs in the background and the dialog follows it -- a bar while the
  release downloads, then the signature check, the install and the restart
  -- and the page reloads by itself once the panel is back on the new
  version. Closing the dialog or reloading the page does not stop it; the
  tag reads *Updating…* until it is done. A second install meanwhile is
  refused, an interrupted download is named as one, and a panel that does
  not come back within three minutes is said so, with the command to look.
  Tested end to end on Linux: a download of a minute and a half, a reload in
  the middle of it, the restart, and the page landing on the new version
  still signed in.
- **Backup archives are no longer cut off.** An upload was given thirty
  seconds to arrive and a download a minute to leave; a large archive over
  a slow link now has thirty minutes.

### Changed
- **The overview bar is laid out as the classic panel's**: at the start, a
  pill with the tunnels' state and this panel's version, and the *Update
  v…* tag right beside it; at the end, the buttons.
- **Backup & Restore works as the classic panel's**: two lines, one button
  each. *Download backup* takes a fresh archive and saves it to your device
  in one click -- it used to take one on the server and offer the newest
  archive as a second step, which could be an older automatic one.
  *Choose a file* shows the archive's name and size, what will happen, and
  the one option that matters when moving servers, before anything
  changes; the upload shows its progress, and the page reloads once the
  panel is back with the restored data instead of after a guessed seven
  seconds. A file that is not a backup is refused with the reason, and
  nothing changes.
- Asking a node to update now returns as soon as the node has started, and
  says so; older nodes still answer as before.

## [2.3.0] — 2026-09-28

### Added
- **"Update v…" on the overview.** While a newer release is published, an
  amber tag beside the panel's version names it; it opens the update dialog,
  with the release notes and the button that installs it -- fetched by the
  panel, checked against the signing key built into it, then a restart. The
  dot on the version alone was easy to miss.

### Changed
- The release list is asked at most every half hour, and after a failure at
  most every five minutes, instead of on every visit to the overview: GitHub
  allows sixty questions an hour, and a server that cannot reach it no longer
  waits on it each time. Opening the update dialog always asks afresh.

### Fixed
- Versions are ordered by number: 2.10.0 comes after 2.9.0, a pre-release is
  behind its release, and a panel ahead of the newest release is no longer
  offered that release as an update.

## [2.2.2] — 2026-09-28

### Fixed
- **The clients pager is Ant's again.** It sits at the right end under the
  table and reads left to right in every language: the total, the arrows,
  the pages and the page size. The page-size box used to stretch across the
  whole row and push the pager to the other side. Only the current page has
  a border; the other pages and the arrows are bare until hovered.
- "25 / page" keeps the number first in Persian, where "۲۵ / صفحه" used to
  turn round, and page numbers use the same digits as the total.

## [2.2.1] — 2026-09-28

### Changed
- **A paused reseller's customers read "Paused"**, a short tag that fits the
  column, and their switch shows off and cannot be flipped, since flipping it
  could not bring them back. The tag's tooltip says why, and whether the
  customer's own switch is on -- that is, whether they come back with the
  reseller. Nothing is written to the customer: their own setting is kept.

## [2.2.0] — 2026-09-28

### Added
- **Resellers, many at once.** A box on every reseller, and one in the header
  for everything the filters show; the toolbar then acts on the selection:
  switch on, switch off, extend the term, start the allowance again, set the
  traffic allowance, set the customer limit, add or remove servers, and
  delete -- asking, as for one reseller, what becomes of their customers.
  Switching off asks first and says how many customers go off. Each reseller
  goes through the same checks as when changed alone; one that cannot be
  changed does not stop the rest, and the answer names it with the reason.
  `POST /api/admins/bulk`.
- Extending in bulk adds the days to a running term, restarts an ended one
  from today, adds them to a term on hold, and leaves a reseller with no end
  date without one.
- **Customers: reset traffic and new keys for a selection.** Both ask first;
  new keys for five or more asks you to type how many. New keys replace every
  file and the subscription link of each selected customer, one at a time,
  and name any that failed.

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

[Unreleased]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.6.1...HEAD
[2.6.1]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.6.0...v2.6.1
[2.6.0]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.5.2...v2.6.0
[2.5.2]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.5.1...v2.5.2
[2.5.1]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.5.0...v2.5.1
[2.5.0]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.4.1...v2.5.0
[2.4.1]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.4.0...v2.4.1
[2.4.0]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.3.6...v2.4.0
[2.3.6]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.3.5...v2.3.6
[2.3.5]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.3.4...v2.3.5
[2.3.4]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.3.3...v2.3.4
[2.3.3]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.3.2...v2.3.3
[2.3.2]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.3.1...v2.3.2
[2.3.1]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.3.0...v2.3.1
[2.3.0]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.2.2...v2.3.0
[2.2.2]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.2.1...v2.2.2
[2.2.1]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.2.0...v2.2.1
[2.2.0]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.1.2...v2.2.0
[2.1.2]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.1.1...v2.1.2
[2.1.1]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.1.0...v2.1.1
[2.1.0]: https://github.com/AbolfazlTafakori/w-ui/compare/v2.0.0...v2.1.0
[2.0.0]: https://github.com/AbolfazlTafakori/w-ui/compare/v1.1.0...v2.0.0
[1.1.0]: https://github.com/AbolfazlTafakori/w-ui/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/AbolfazlTafakori/w-ui/releases/tag/v1.0.0
