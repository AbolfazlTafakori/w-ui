"""The outbound at the top, on a real 3x-ui node: with 'blocked' at the top,
this panel's customers there -- made here, or brought in -- go nowhere and the
3x-ui operator's own user still goes out; with direct at the top again, all of
them do.

usage: python3 egress.py
Needs a customer of this panel on that node, one brought in (import_users.py
leaves alice-v), and OWN_EMAIL, a client of that panel's own operator.
Puts 'blocked' at the top of W-UI's outbounds for a minute: every customer of
this panel is cut off for it.
"""
import json
import time

from lab import WUI, XUI, Checks, Dialer, env

check = Checks()
wui = WUI()
x3 = XUI.v3()
node = wui.nodes()[env('NODE_XUI3', 'xui-v3')]['id']
tunnels = {i['id']: i for i in wui.call('GET', '/api/interfaces') if i.get('nodeId') == node}

made = brought = None
for c in wui.customers():
    full = wui.call('GET', f"/api/clients/{c['id']}")
    for a in full['accounts']:
        if a['interfaceId'] in tunnels and not a.get('adopted') and not made:
            made = full['id']
        if a.get('adopted') == 'alice-v' and not brought:
            brought = full['id']
if not made or not brought:
    raise SystemExit('needs a customer made here on that node, and alice-v brought in')
users = {
    'made here': next(l for l in wui.subscription(made) if l.startswith('vless://')),
    'brought in': next(l for l in wui.subscription(brought) if l.startswith('vless://')),
    "the operator's own": x3.call('clients/links/' + env('OWN_EMAIL'))['obj'][0],
}
dial = Dialer(wui, port_base=25300)
ports = {name: dial.open(link) for name, link in users.items()}


def rules():
    obj = x3.call('xray/', {})['obj']
    obj = json.loads(obj) if isinstance(obj, str) else obj
    cfg = obj.get('xraySetting', obj)
    cfg = json.loads(cfg) if isinstance(cfg, str) else cfg
    return [r for r in cfg['routing']['rules'] if str(r.get('outboundTag', '')).startswith('wui-out')]


try:
    for name, port in ports.items():
        check(f'before: {name} goes out', dial.answers(port))
    obs = {o['tag']: o['id'] for o in wui.call('GET', '/api/outbounds')}
    try:
        wui.call('POST', '/api/outbounds/order', {'ids': [obs['blocked'], obs['direct']]})
        time.sleep(45)  # a round, and its xray restarted
        ours = rules()
        check("its xray has this panel's rule, under its own tag", len(ours) == 1 and ours[0]['outboundTag'] != 'wui-out', ours)
        check('  matching its clients by pattern, and alice-v by name',
              bool(ours) and ours[0]['user'][0].startswith('regexp:^wui-') and 'alice-v' in ours[0]['user'])
        check('blocked at the top: made here goes nowhere', not dial.answers(ports['made here']))
        check('blocked at the top: brought in goes nowhere', not dial.answers(ports['brought in']))
        check("blocked at the top: the operator's own still goes out", dial.answers(ports["the operator's own"]))
    finally:
        wui.call('POST', '/api/outbounds/order', {'ids': [obs['direct'], obs['blocked']]})
    time.sleep(45)
    check('direct again: the rule is gone', not rules())
    for name, port in ports.items():
        check(f'direct again: {name} goes out', dial.answers(port))
finally:
    dial.close()
check.done()
