"""Makes every inbound of cases.json through W-UI, as its form would, and
checks each where it was made: its protocol, transport and security there,
the panel's xray still running after it, its Pasarguard host, and the tunnel
W-UI holds for it.

usage: python3 create_matrix.py cases.json
"""
import json
import sys
import time

from lab import WUI, XUI, Pasarguard, Checks, env

check = Checks()
wui = WUI()
x3, x2, pg = XUI.v3(), XUI.v2(), Pasarguard()
nodes = wui.nodes()
node_of = {'xui': nodes[env('NODE_XUI3', 'xui-v3')], 'xui-old': nodes[env('NODE_XUI2', 'xui-v2')],
           'pasarguard': nodes[env('NODE_PG', 'pg')]}
cases = json.load(open(sys.argv[1]))
# Kinds made for the operator's own use, with no customers and so no stream
# of their own on 3x-ui 3, and no host on Pasarguard.
no_customers = {'mixed', 'tunnel', 'http'}

# One inbound also carries 3x-ui's own allowance, expiry and monthly reset.
EXPIRY = int((time.time() + 30 * 86400) * 1000)
for c in cases:
    if c['name'] == 't-vless-ws':
        c['xray'].update(total=50 * 1024 ** 3, expiryTime=EXPIRY, trafficReset='monthly', trafficResetDay=5)

for c in cases:
    label = f"made {c['panel']} {c['name']} ({c['protocol']} {c['network']}/{c['security']})"
    try:
        made = wui.call('POST', '/api/interfaces', {'name': c['name'], 'nodeId': node_of[c['panel']]['id'], 'protocol': 'xray',
                                                    'listenPort': c['port'], 'xray': c['xray']})
        made = made.get('interface', made)
        check(label, True, f"customers={(made.get('xui') or {}).get('customers')}")
    except RuntimeError as e:
        check(label, False, str(e)[:300])
        continue
    time.sleep(2)
    panel = {'xui': x3, 'xui-old': x2}.get(c['panel'])
    if panel:
        st = panel.xray_state()
        check(f"  xray running after {c['name']}", st.get('state') == 'running', st.get('errorMsg', '')[:200])

time.sleep(5)
there = {ib['remark']: ib for ib in x3.inbounds()}
there.update({ib['remark']: ib for ib in x2.inbounds()})
core = {i['tag']: i for co in pg.call('/api/cores')['cores'] for i in co['config'].get('inbounds', [])}
hosts = {h['inbound_tag']: h for h in pg.call('/api/hosts')}


def stream_of(ib):
    st = ib.get('streamSettings')
    if isinstance(st, str):
        st = json.loads(st) if st else {}
    return st or {}


for c in cases:
    ib = core.get(c['name']) if c['panel'] == 'pasarguard' else there.get(c['name'])
    if ib is None:
        check(f"on its panel: {c['name']}", False, 'not there')
        continue
    st = stream_of(ib)
    want = c['xray'].get('streamSettings', {})
    plain = c['protocol'] in no_customers and c['panel'] in ('xui', 'pasarguard')
    net_ok = plain or st.get('network') in (want.get('network'), 'raw' if want.get('network') == 'tcp' else None)
    sec_ok = plain or st.get('security') == want.get('security')
    check(f"on its panel: {c['name']}", ib.get('protocol') == c['protocol'] and net_ok and sec_ok,
          f"{ib.get('protocol')} {st.get('network')}/{st.get('security')} port {ib.get('port')}")
    if c['panel'] == 'pasarguard':
        has_host = c['name'] in hosts
        check(f"  host for {c['name']}", has_host == (c['protocol'] not in no_customers), 'a host' if has_host else 'no host')
    if c['name'] == 't-vless-ws':
        check('  3x-ui keeps the inbound allowance, expiry and reset',
              ib.get('total') == 50 * 1024 ** 3 and ib.get('expiryTime') == EXPIRY and ib.get('trafficReset') == 'monthly' and ib.get('trafficResetDay') == 5,
              f"total={ib.get('total')} expiry={ib.get('expiryTime')} reset={ib.get('trafficReset')}/{ib.get('trafficResetDay')}")

held = {i['name']: i for i in wui.call('GET', '/api/interfaces') if i['protocol'] == 'xray'}
for c in cases:
    i = held.get(c['name'])
    want = c['protocol'] not in no_customers
    check(f"held here: {c['name']}", i is not None and (i.get('xui') or {}).get('customers') == want,
          f"customers={(i or {}).get('xui', {}).get('customers')}")
check.done()
