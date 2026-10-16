# Real-panel tests of 3x-ui and Pasarguard nodes

`go test ./internal/nodes` runs the rounds against 3x-ui and Pasarguard
panels kept in memory. These scripts run the same things against real panels
and real xray, with traffic passing: what a fake cannot show — that a real
3x-ui accepts what the form sends, that its xray keeps running, that a link
dials and a rule routes.

They need a W-UI panel with three nodes: a 3x-ui 3.x (by token), a 3x-ui 2.x
(by password) and a Pasarguard (by API key) with a node of its own. Run them on
the machine those panels are on, with `xray` installed and `echo.py` running
there.

## The environment

| Name | What |
|---|---|
| `WUI_URL`, `WUI_USER`, `WUI_PASS` | the W-UI panel, with its secret path |
| `NODE_XUI3`, `NODE_XUI2`, `NODE_PG` | the nodes' names in it (`xui-v3`, `xui-v2`, `pg`) |
| `XUI3_URL`, `XUI3_TOKEN` | the 3x-ui 3 panel, with its secret path |
| `XUI2_URL`, `XUI2_USER`, `XUI2_PASS`, `XUI2_SUB_URL` | the 3x-ui 2 panel, and its subscription service's address up to the id |
| `PG_URL`, `PG_API_KEY` | the Pasarguard panel |
| `NODE_IP` | the address traffic leaves the nodes from |
| `ECHO_URL` | `echo.py`, as `http://host:18099` |
| `TLS_CERT_FILE`, `TLS_KEY_FILE`, `TLS_SERVER_NAME` | a certificate on the 3x-ui 3 host, for its TLS inbounds |
| `DIAL_REWRITE` | inbounds reached at another address by their ports, as `{"33002-33007": "172.17.0.2"}` for a panel in docker |
| `DIAL_PINS` | a pasted certificate's SHA-256 by the ports it is on, pinned when dialling, as `{"33002-33007,36001-36007": "ab12…"}` |
| `X3_VLESS`, `X3_TROJAN`, `X3_REALITY`, `X3_VMESS` | 3x-ui 3 inbounds, by remark, for `import_users.py` |
| `X2_VLESS`, `X2_VMESS` | 3x-ui 2 inbounds, by remark |
| `PG_OWNER_TAGS` | Pasarguard inbound tags for the owners' group, comma-separated |
| `OWN_EMAIL` | a client of the 3x-ui 3 operator's own, for `egress.py` |

## The scripts

```sh
python3 echo.py &                                  # the address a request came from, and 2 MB
node gen_cases.mjs cert.pem key.pem > cases.json   # every inbound kind, built by the form's own builder
python3 create_matrix.py cases.json                # made through W-UI, checked on each panel
python3 customer_matrix.py cases.json              # a customer on all of them, traffic through every link
python3 import_users.py                            # the panels' own users brought in, their traffic never stopping
python3 egress.py                                  # the top outbound: this panel's customers and nobody else's
python3 cleanup.py cases.json                      # all of it taken away
```

Each prints a line per check and ends `N passed, M failed`, exiting non-zero
when anything failed.

`egress.py` puts *blocked* at the top of the outbounds for about a minute:
every customer of the panel is cut off while it does. Run it on a test panel.
