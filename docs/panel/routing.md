# Routing

Which traffic goes where. Four tabs.

## Basic

The default outbound, the domain strategy, the blocks 3x-ui applies by default — BitTorrent on its well-known ports, private ranges — plus your own blocked and direct lists of domains, addresses, `geoip:xx` countries and named groups, and the IPv4-only list for services that misbehave over IPv6.

## Rules

Ordered rules, each matching by source (customers, groups), inbound, network, destination (domains, addresses, geo), and sending to an outbound or a balancer. Drag to reorder; the first match wins. Every rule can be enabled or disabled without deleting it.

On a phone the table becomes cards: inbound → outbound, with the criteria as chips.

## Balancers

See [Outbounds → Balancers](/panel/outbounds#balancers).

## Route tester

Type an address or a domain and a source, and the panel tells you which rule would catch it and where it would go — before a customer finds out.
