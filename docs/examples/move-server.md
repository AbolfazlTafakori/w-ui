---
description: "Take the panel, its customers and its keys to another server without reissuing a single config."
---

# Move the panel to another server

Because the interfaces' keys and ports live in the database, a restored panel is the same panel to every customer.

**1. Old server** — take a backup: Overview → **Backup & Restore** → *Download backup*, or `w-ui` → 25 → 1 and copy the file off.

**2. New server** — install it as usual. Its port, URL path and certificate are its own: a restore never changes how the panel is reached, so they do not have to match the old server's.

**3. Restore** — on the new panel, Overview → **Backup & Restore** → *Choose a file*, pick the archive, and press **Restore**. Or copy it over and run `w-ui` → 25 → 2. The panel checks the archive, saves what is there now, restarts on the restored data, and the page reloads once it is back. Sign in with the **old** server's administrator: the accounts come from the archive.

Do not unpack the archive by hand. Its paths are relative to the data directory, so `tar … -C /` puts the database where the panel never looks, and it skips the checks and the clean-up a restore does.

**4. Addresses** — *Keep this server's addresses* is on by default: each tunnel keeps the endpoint this server already has. If the new server has no tunnels yet, the archive's endpoints stay; then, if customers' configs carry a domain, move the DNS record, and if they carry the old IP, edit each interface's *Endpoint host* (and each host in Hosts) to the new address. Customers re-fetch through their subscription link.

**5. Certificate** — the new server keeps its own. If customers reach the subscription through a domain, issue one for it on the new server: `w-ui` → 20 → 1; for the IP, → 6.

The old server can stay up during the switch — both enforce the same limits from the same data, and a customer on either is counted on that one.
