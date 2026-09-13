---
description: "One static, CGO-free binary at /usr/local/bin/wui with the frontend embedded. Run with no arguments it is the panel; with a subcommand it is a tool the script and the installer use."
---

# The `wui` binary

One static, CGO-free binary at `/usr/local/bin/wui` with the frontend embedded. Run with no arguments it is the panel; with a subcommand it is a tool the script and the installer use.

```
wui                              run the panel
wui setting show                 print the effective configuration
wui setting show --json          the same, as JSON
wui setting set [flags]          change where the panel answers (applied at the next start)
wui setting reset                forget every panel setting; the admin account and the customers are kept
wui admin reset [flags]          reset the administrator account
wui token issue --name NAME      mint an API token, printed once
wui version                      print the version
wui keygen                       make a release-signing key pair
wui sign <binary>                sign a build, for a release
```

## `setting set`

```
--listen ADDR      the address to bind (0.0.0.0 for every address)
--port N           the port
--base-path PATH   the secret path the panel is served under
--cert FILE        certificate to serve TLS with
--key FILE         its private key
--no-tls           forget the certificate and serve plain HTTP
```

The settings page's values win over the environment at start, so this is where a change has to land to stick. `setting show` prints the effective result — `listen`, `listenIP`, `port`, `basePath`, `scheme`, `cert`, `key` — in the lines the script parses.

## `admin reset`

```
--username NAME    the administrator's name (default: keep the current one)
--password PASS    the new password (default: generate one and print it)
--password-stdin   read the password from standard input
--quiet            print nothing on success
```

## Signals

`SIGHUP` restarts every tunnel from its stored configuration without stopping the panel; `systemctl reload wui` sends it. `SIGTERM` stops the panel and leaves the tunnels running.
