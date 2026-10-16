"""A customer on every inbound of the matrix that takes customers: a client
there, its links in the customer's one subscription here, and traffic through
every link xray can dial, out at the node's own address.

usage: python3 customer_matrix.py cases.json
Run after create_matrix.py. Leaves the customer, named matrix, for
cleanup.py.
"""
import json
import sys
import time

from lab import WUI, Checks, Dialer, env, link_name

check = Checks()
wui = WUI()
cases = {c['name']: c for c in json.load(open(sys.argv[1]))}
tunnels = [i for i in wui.call('GET', '/api/interfaces') if i['name'] in cases and (i.get('xui') or {}).get('customers')]
for c in wui.customers():
    if c['name'] == 'matrix':
        wui.call('DELETE', f"/api/clients/{c['id']}")
time.sleep(1)
cust = wui.call('POST', '/api/clients', {'name': 'matrix', 'interfaceIds': [i['id'] for i in tunnels], 'quotaBytes': 20 * 1024 ** 3, 'deviceLimit': 1})
check('a customer on every inbound that takes one', len(cust['accounts']) == len(tunnels), f"{len(cust['accounts'])} files on {len(tunnels)} inbounds")

# The clients are made by a round, and their links read by the next.
time.sleep(80)
links = wui.subscription(cust['id'])
print(f'{len(links)} links:', sorted(l.split(':')[0] for l in links))
# Kinds a proxy app has no URI for are on the customer's page instead.
no_uri = {'mtproto', 'tuic', 'masque', 'wireguard', 'amneziawg'}
names = [link_name(l) for l in links]
for t in tunnels:
    name = t['name']
    has = any(name in n for n in names)
    check(f'link for {name}', has or cases[name]['protocol'] in no_uri)

# Out at the node's own address; a panel in docker is reached, and seen, at
# its address there.
leaves = {env('NODE_IP'), *json.loads(env('DIAL_REWRITE', '{}')).values()}
dial = Dialer(wui)
try:
    opened = []
    for link in links:
        port = dial.open(link)
        if port is None:
            print('  (not dialled by xray)', link[:50])
            continue
        opened.append((port, link_name(link) or link[:30]))
    for port, label in opened:
        ip, size = dial.passes(port)
        check(f'traffic through {label[:40]}', ip in leaves and size == '2097152', f'ip={ip or "-"} bytes={size}')
finally:
    dial.close()
check.done()
