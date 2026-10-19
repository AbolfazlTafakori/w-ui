---
description: "One static, CGO-free binary at /usr/local/bin/wui with the frontend embedded. Run with no arguments it is the panel; with a subcommand it is a tool the script and the installer use."
---

# The `wui` binary

One static, CGO-free binary at `/usr/local/bin/wui`, with the frontend embedded. Run with no arguments, it is the panel. With a subcommand, it is a tool the script and the installer use.

```
wui                              run the panel
wui setting show                 print the effective configuration
wui setting show --json          the same, as JSON
wui setting set [flags]          change where the panel answers (applied at the next start)
wui setting reset                forget every panel setting; the admin account and the customers are kept
wui admin reset [flags]          reset the administrator account
wui token issue --name NAME      mint an API token, printed once
wui backup create [--dir DIR]    take a backup, the same archive the panel takes
wui backup list [--dir DIR]      the backups in that directory
wui backup restore FILE          stage a restore, applied at the next start
wui version                      print the version
wui keygen                       make a release-signing key pair
wui sign <binary>                sign a build, for a release
wui verify <binary> <sig>        check a build against the key this panel was built with
```

Four more subcommands are run by systemd, not by hand: `apply-update` (by `wui-update.service`, to install an update the panel downloaded), `l2tp-agent` (by `wui-l2tp.service`, to run the L2TP/IPsec, PPTP, IKEv2 and OpenConnect tunnels), `pptpd` (started by that agent), and `firewall-sync` (by `wui-firewall.service`, to let the tunnels' ports through ufw or firewalld).

## `setting set`

```
--listen ADDR      the address to bind (0.0.0.0 for every address)
--port N           the port
--base-path PATH   the secret path the panel is served under
--cert FILE        certificate to serve TLS with
--key FILE         its private key
--no-tls           forget the certificate and serve plain HTTP
--sub-enable       serve customers' subscription links
--sub-disable      stop serving them
--sub-port N       a port of the subscription service's own (0: the panel's)
--sub-listen ADDR  the address it binds
--sub-cert FILE    certificate for that port (default: the panel's)
--sub-key FILE     its private key
```

The settings page's values win over the environment at start, so this is where a change has to land to stick. `setting show` prints the effective result, in the lines the script parses: `listen`, `listenIP`, `port`, `basePath`, `scheme`, `cert`, `key`, `subEnabled`, `subPort`, `subPath`, `dbDriver` and `dbSource`, with the password redacted.

## `backup`

`create` writes the same archive the panel's scheduler writes: the SQLite snapshot when there is one, the server files, and a portable JSON dump of every table, so the file restores into a panel on either database engine and into a newer W-UI. It is encrypted when a backup password is set.

`restore` copies the archive in, checks it, keeps a copy of the current data beside it, and stages it; the next start applies it. Pass `--move-addresses` to take the archive's server addresses instead of keeping this machine's.

Both need the panel's environment, which the `w-ui` menu supplies, as does `/etc/wui/db.env`. See [Backup and restore](/operations/backup-restore).

## `admin reset`

```
--username NAME    the administrator's name (default: keep the current one)
--password PASS    the new password (default: generate one and print it)
--password-stdin   read the password from standard input
--reset-two-factor forget the two-factor secret, so the account signs in with the password alone
--quiet            print nothing on success
```

## Signals

`SIGHUP` restarts every tunnel from its stored configuration without stopping the panel; `systemctl reload wui` sends it. `SIGTERM` stops the panel and leaves the tunnels running.
