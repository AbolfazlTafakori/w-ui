---
description: "Take the panel, its customers and its keys to another server without reissuing a single config."
---

# Move the panel to another server

Because the interfaces' keys and ports live in the database, a restored panel is the same panel to every customer.

**1. Old server** — take a backup: Overview → Backup → *Back up now*, or `w-ui` → 25 → 1. Copy it off.

**2. New server** — install, with the same port and path (`--port` / `--path`), then:

```bash
systemctl stop wui
tar xzf wui-backup-….tar.gz -C /
chown -R wui:wui /var/lib/wui
systemctl start wui
```

**3. Addresses** — if customers' configs carry a domain, move the DNS record. If they carry the old IP, edit each interface's *Endpoint host* (and each host in Hosts) to the new address; customers re-fetch through their subscription link.

**4. Certificate** — if the old one was for the domain, `w-ui` → 20 → 1 again on the new server; for the IP, → 6.

The old server can stay up during the switch — both enforce the same limits from the same data, and a customer on either is counted on that one.
