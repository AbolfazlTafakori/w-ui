"""A 3x-ui 3, a 3x-ui 2 and a Pasarguard panel's own users brought in as
customers, with traffic running through their own links the whole time:
nothing of theirs changes there but what should, and nothing of theirs stops.

usage: python3 import_users.py
Makes users named alice-v, bob, carol (3x-ui 3), owner-2, owner-2b (3x-ui 2)
and dave, erin in a group owners (Pasarguard); cleanup.py removes them.
"""
import base64
import json
import threading
import time
import urllib.request
import uuid

from lab import INSECURE, WUI, XUI, Pasarguard, Checks, Dialer, env

check = Checks()
wui = WUI()
x3, x2, pg = XUI.v3(), XUI.v2(), Pasarguard()
nodes = wui.nodes()
NODES = {'xui3': nodes[env('NODE_XUI3', 'xui-v3')]['id'], 'xui2': nodes[env('NODE_XUI2', 'xui-v2')]['id'], 'pg': nodes[env('NODE_PG', 'pg')]['id']}
DAY = 86400
now_ms = int(time.time() * 1000)


def x3_inbound(remark):
    return next(ib['id'] for ib in x3.inbounds() if ib['remark'] == remark)


def x2_inbound(remark):
    return next(ib['id'] for ib in x2.inbounds() if ib['remark'] == remark)


print("--- the owners' users")
for (_, email) in list(x3.clients()):
    if email in ('alice-v', 'bob', 'carol'):
        x3.call('clients/del/' + email, {})
for (ib, email), c in list(x2.clients().items()):
    if email in ('owner-2', 'owner-2b'):
        x2.call(f'inbounds/{ib}/delClient/{c["id"]}', form={})
for u in pg.call('/api/users')['users']:
    if u['username'] in ('dave', 'erin'):
        pg.call('/api/user/' + u['username'], 'DELETE')
for g in pg.call('/api/groups')['groups']:
    if g['name'] == 'owners':
        pg.call(f"/api/group/{g['id']}", 'DELETE')

# 3x-ui 3.1 and later: one client on two inbounds, its subscription id its own.
X3_VLESS, X3_TROJAN, X3_REALITY, X3_VMESS = (x3_inbound(env(n)) for n in ('X3_VLESS', 'X3_TROJAN', 'X3_REALITY', 'X3_VMESS'))
A_ID, A_PW, B_ID, C_ID = str(uuid.uuid4()), uuid.uuid4().hex[:16], str(uuid.uuid4()), str(uuid.uuid4())
x3.call('clients/add', {'client': {'id': A_ID, 'password': A_PW, 'email': 'alice-v', 'enable': True, 'limitIp': 2, 'totalGB': 10 * 1024 ** 3,
                                   'expiryTime': now_ms + 20 * DAY * 1000, 'subId': 'alicesub0001', 'comment': 'Alice', 'flow': ''},
                        'inboundIds': [X3_VLESS, X3_TROJAN]})
x3.call('clients/add', {'client': {'id': B_ID, 'email': 'bob', 'enable': True, 'expiryTime': -7 * DAY * 1000, 'subId': 'bobsub000001',
                                   'flow': 'xtls-rprx-vision'}, 'inboundIds': [X3_REALITY]})
x3.call('clients/add', {'client': {'id': C_ID, 'email': 'carol', 'enable': False, 'totalGB': 3 * 1024 ** 3, 'subId': 'carolsub0001'},
                        'inboundIds': [X3_VMESS]})
x3.call('server/restartXrayService', {})
# 3x-ui 2: one client per inbound, two of them sharing a subscription id.
O2, O2B = str(uuid.uuid4()), str(uuid.uuid4())
for inbound, cid, email in ((x2_inbound(env('X2_VLESS')), O2, 'owner-2'), (x2_inbound(env('X2_VMESS')), O2B, 'owner-2b')):
    x2.call('inbounds/addClient', form={'id': inbound, 'settings': json.dumps({'clients': [{
        'id': cid, 'email': email, 'enable': True, 'limitIp': 0, 'totalGB': 2 * 1024 ** 3, 'expiryTime': now_ms + 5 * DAY * 1000,
        'subId': 'owner2sub001', 'flow': ''}]})})
# Pasarguard: a group of two inbounds, a user with a limit and an expiry, and
# one on hold.
grp = pg.call('/api/group', 'POST', {'name': 'owners', 'inbound_tags': env('PG_OWNER_TAGS').split(',')})
pg.call('/api/user', 'POST', {'username': 'dave', 'proxy_settings': {}, 'group_ids': [grp['id']], 'data_limit': 5 * 1024 ** 3,
                              'expire': int(time.time()) + 10 * DAY, 'status': 'active', 'note': "the owner's"})
pg.call('/api/user', 'POST', {'username': 'erin', 'proxy_settings': {}, 'group_ids': [grp['id']], 'status': 'on_hold',
                              'on_hold_expire_duration': 30 * DAY, 'expire': 0})
time.sleep(8)
before2 = x2.clients()
dave0 = pg.call('/api/user/dave')

# Their own links, from their own panels.
own = {'alice-v': x3.call('clients/links/alice-v')['obj']}
sub = dave0['subscription_url'] if dave0['subscription_url'].startswith('http') else env('PG_URL') + dave0['subscription_url']
own['dave'] = [l for l in urllib.request.urlopen(sub.rstrip('/') + '/links', context=INSECURE, timeout=30).read().decode().split() if l.startswith('vless://')]
own['owner-2'] = [l for l in base64.b64decode(urllib.request.urlopen(env('XUI2_SUB_URL').rstrip('/') + '/owner2sub001', timeout=15).read()).decode().split()
                  if '://' in l]
print('  their own links:', {k: len(v) for k, v in own.items()})

# Traffic through each, all along.
dial = Dialer(wui, port_base=25100)
stop = threading.Event()
fails, oks = {}, {}


def keep_going(name, port):
    fails[name], oks[name] = [], 0
    while not stop.is_set():
        if dial.answers(port):
            oks[name] += 1
        else:
            fails[name].append(time.strftime('%H:%M:%S'))
        time.sleep(0.5)


threads = []
for name, links in own.items():
    port = dial.open(links[0])
    t = threading.Thread(target=keep_going, args=(name, port), daemon=True)
    t.start()
    threads.append(t)
time.sleep(30)  # a round reads the new users

cands = {}
for key, node in NODES.items():
    for c in wui.call('GET', f'/api/nodes/{node}/users'):
        cands[c['key']] = (key, c)
check('a 3x-ui client on two inbounds is one customer', len(cands.get('alicesub0001', ('', {}))[1].get('inbounds', [])) == 2)
a = cands.get('alicesub0001', ('', {}))[1]
check('  with its allowance, expiry and device limit', a.get('quotaBytes') == 10 * 1024 ** 3 and a.get('expiresAt') and a.get('deviceLimit') == 2, a)
check('a 3x-ui start-on-first-use expiry is a duration', cands.get('bobsub000001', ('', {}))[1].get('durationDays') == 7)
check('a switched-off user is offered as off', cands.get('carolsub0001', ('', {}))[1].get('enabled') is False)
check('3x-ui 2 clients sharing a subscription are one customer', len(cands.get('owner2sub001', ('', {}))[1].get('inbounds', [])) == 2)
check('Pasarguard users are offered with limit and expiry', cands.get('dave', ('', {}))[1].get('quotaBytes') == 5 * 1024 ** 3)
check('a Pasarguard on-hold user is a duration', cands.get('erin', ('', {}))[1].get('durationDays') == 30)

made = {}
for key, node in NODES.items():
    keys = [k for k, (n, _) in cands.items() if n == key and k in ('alicesub0001', 'bobsub000001', 'carolsub0001', 'owner2sub001', 'dave', 'erin')]
    for r in wui.call('POST', f'/api/nodes/{node}/users/import', {'keys': keys}):
        made[r['key']] = r
        check(f"brought in {r['key']}", r.get('clientId'), r.get('error', ''))
again = wui.call('POST', f"/api/nodes/{NODES['pg']}/users/import", {'keys': ['dave']})
check('one brought in already is not brought in twice', again and again[0].get('error'))
time.sleep(45)  # rounds: limits cleared there, links read


def customer(key):
    return wui.call('GET', f"/api/clients/{made[key]['clientId']}")


for key, want in (('alicesub0001', A_ID), ('owner2sub001', O2), ('owner2sub001', O2B), ('dave', None)):
    body = '\n'.join(wui.subscription(made[key]['clientId']))
    plain = body + ''.join(base64.b64decode(l[8:] + '==').decode(errors='ignore') for l in body.split() if l.startswith('vmess://'))
    check(f"this panel's subscription for {key} carries its links", (want in plain) if want else 'vless://' in body)

after3, after2 = x3.clients(), x2.clients()
av = after3.get((X3_VLESS, 'alice-v'), {})
check('alice-v there: same id, limits cleared, on', av.get('id') == A_ID and av.get('totalGB') == 0 and av.get('expiryTime') == 0 and av.get('enable'))
check('  its own fields kept', av.get('subId') == 'alicesub0001' and av.get('comment') == 'Alice' and av.get('limitIp') == 2)
check('alice-v on its second inbound too', after3.get((X3_TROJAN, 'alice-v'), {}).get('password') == A_PW)
check('bob there: same id, delay-start cleared', after3.get((X3_REALITY, 'bob'), {}).get('id') == B_ID and after3[(X3_REALITY, 'bob')].get('expiryTime') == 0)
check('carol there stays off', after3.get((X3_VMESS, 'carol'), {}).get('enable') is False)
o2 = after2.get((x2_inbound(env('X2_VLESS')), 'owner-2'), {})
check('owner-2 on 3x-ui 2: same id, cleared', o2.get('id') == O2 and o2.get('totalGB') == 0 and o2.get('expiryTime') == 0)
check("3x-ui 2's other clients untouched", all(after2.get(k) == v for k, v in before2.items() if k[1] not in ('owner-2', 'owner-2b')))
dv = pg.call('/api/user/dave')
check('dave on Pasarguard: limits cleared, active, groups his own',
      dv['data_limit'] in (None, 0) and dv['expire'] is None and dv['status'] == 'active' and dv['group_ids'] == dave0['group_ids'])
check('  his credentials unchanged', dv['proxy_settings'] == dave0['proxy_settings'])
check('erin on Pasarguard is active, her clock kept here', pg.call('/api/user/erin')['status'] == 'active')

before_off = {k: len(v) for k, v in fails.items()}
wui.call('POST', '/api/clients/bulk', {'action': 'disable', 'ids': [made['alicesub0001']['clientId'], made['dave']['clientId']]})
time.sleep(30)
check('disabled here: off on 3x-ui', x3.clients().get((X3_VLESS, 'alice-v'), {}).get('enable') is False)
check('disabled here: off on Pasarguard', pg.call('/api/user/dave')['status'] == 'disabled')
wui.call('POST', '/api/clients/bulk', {'action': 'enable', 'ids': [made['alicesub0001']['clientId'], made['dave']['clientId']]})
time.sleep(30)
check('enabled here: on again on 3x-ui', x3.clients().get((X3_VLESS, 'alice-v'), {}).get('enable') is True)
check('enabled here: on again on Pasarguard', pg.call('/api/user/dave')['status'] == 'active')

bob = x3.clients()[(X3_REALITY, 'bob')]
bob['enable'] = False
x3.call('clients/update/bob', bob)
time.sleep(30)
check('switched off there by hand: kept off, here too',
      x3.clients()[(X3_REALITY, 'bob')]['enable'] is False and not any(a['enabled'] for a in customer('bobsub000001')['accounts']))

used = customer('alicesub0001')['usedBytes']
time.sleep(30)
check('what alice spends is counted here', customer('alicesub0001')['usedBytes'] > used)

wui.call('DELETE', f"/api/clients/{made['carolsub0001']['clientId']}")
time.sleep(25)
check('a customer removed here is left on its panel', (X3_VMESS, 'carol') in x3.clients())

stop.set()
for t in threads:
    t.join(timeout=10)
dial.close()
for name in oks:
    print(f'  traffic {name}: {oks[name]} ok, {len(fails[name])} failed', fails[name][:5])
# Off from here for 30 s on purpose; only then may their traffic stop.
check('their own links carried traffic throughout but while switched off here',
      all(before_off.get(n, 0) == 0 for n in oks) and all(oks[n] > 50 for n in oks), before_off)
json.dump({k: v.get('clientId') for k, v in made.items()}, open('/tmp/xray-nodes-imported.json', 'w'))
check.done()
