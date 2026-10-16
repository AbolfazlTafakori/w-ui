"""Takes away what the other scripts made: the matrix customer and the
customers brought in, the matrix's inbounds on the three panels and its
tunnels here, and the owners' test users.

usage: python3 cleanup.py cases.json
"""
import json
import sys
import time

from lab import WUI, XUI, Pasarguard

wui = WUI()
x3, x2, pg = XUI.v3(), XUI.v2(), Pasarguard()
names = {c['name'] for c in json.load(open(sys.argv[1]))}
test_users = {'alice-v', 'bob', 'carol', 'owner-2', 'owner-2b'}

try:
    imported = json.load(open('/tmp/xray-nodes-imported.json')).values()
except OSError:
    imported = []
for c in wui.customers():
    if c['name'] == 'matrix' or c['id'] in imported:
        wui.call('DELETE', f"/api/clients/{c['id']}")
        print('customer', c['name'])
time.sleep(2)

# The inbounds are their panels'; a tunnel here goes with its inbound there.
for ib in x3.inbounds():
    if ib['remark'] in names:
        print('3x-ui 3 inbound', ib['remark'], x3.call(f"inbounds/del/{ib['id']}", {}).get('success'))
for (ib, email), c in x3.clients().items():
    if email in test_users:
        x3.call('clients/del/' + email, {})
for ib in x2.inbounds():
    if ib['remark'] in names:
        print('3x-ui 2 inbound', ib['remark'], x2.call(f"inbounds/del/{ib['id']}", form={}).get('success'))
for (ib, email), c in x2.clients().items():
    if email in test_users:
        x2.call(f"inbounds/{ib}/delClient/{c['id']}", form={})

for h in pg.call('/api/hosts'):
    if h['inbound_tag'] in names:
        pg.call(f"/api/host/{h['id']}", 'DELETE')
tags_of = {g['id']: g.get('inbound_tags') or [] for g in pg.call('/api/groups')['groups']}
for u in pg.call('/api/users')['users']:
    # A standing user only of the matrix's own inbounds.
    standing = u['username'].startswith('wuikeep_') and any(t in names for g in u['group_ids'] for t in tags_of.get(g, []))
    if u['username'] in ('dave', 'erin') or standing:
        pg.call('/api/user/' + u['username'], 'DELETE')
for g in pg.call('/api/groups')['groups']:
    if g['name'] == 'owners' or any(t in names for t in g.get('inbound_tags') or []):
        pg.call(f"/api/group/{g['id']}", 'DELETE')
for core in pg.call('/api/cores')['cores']:
    ins = core['config'].get('inbounds', [])
    keep = [i for i in ins if i['tag'] not in names]
    if len(keep) != len(ins):
        core['config']['inbounds'] = keep
        pg.call(f"/api/core/{core['id']}?restart_nodes=true", 'PUT',
                {k: core[k] for k in ('name', 'config', 'exclude_inbound_tags', 'fallbacks_inbound_tags') if k in core})
        print('Pasarguard inbounds', len(ins) - len(keep))
print('clean; the tunnels here go within a round')
