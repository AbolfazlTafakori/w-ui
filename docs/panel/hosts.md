# Hosts

The addresses a customer's config points at. Without hosts, every config dials the interface's endpoint. With hosts, one customer fans out to several addresses — a CDN front, a second server, a domain and an IP — and gets one config, one QR and one subscription entry **per host**, the way 3x-ui's hosts work.

## Host groups

Hosts are organised in groups; a group is enabled or disabled as one, and groups can be reordered. Each host has a description, tags, the formats it should be left out of (`conf`, `base64`, `zip`, `page`), and whether its position is shuffled.

## Precedence

A host's address wins; a blank address inherits the interface's endpoint; port `0` inherits the interface's port. OpenVPN gets a single variant, WireGuard one per host.
