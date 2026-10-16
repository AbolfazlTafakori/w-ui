"""What the real-panel tests of 3x-ui and Pasarguard nodes share: where the
panels are, how each is spoken to, a check that is counted, and a link dialled
through xray to see that traffic really passes.

Everything about the panels comes from the environment; nothing is kept here.
See README.md for the names.
"""
import base64
import http.cookiejar
import json
import os
import signal
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

# The panels here are test panels with self-signed certificates.
INSECURE = ssl._create_unverified_context()


def env(name, default=None):
    v = os.environ.get(name, default)
    if v is None:
        sys.exit(f'{name} is not set; see scripts/xray-nodes/README.md')
    return v


def _json(req, timeout=60):
    try:
        with urllib.request.urlopen(req, timeout=timeout, context=INSECURE) as r:
            raw = r.read()
    except urllib.error.HTTPError as e:
        raise RuntimeError(f'{req.get_method()} {req.full_url}: {e.code} {e.read().decode(errors="replace")[:300]}')
    return json.loads(raw) if raw else None


class WUI:
    """The W-UI panel under test: WUI_URL (with its secret path), WUI_USER, WUI_PASS."""

    def __init__(self):
        self.base = env('WUI_URL').rstrip('/')
        self.token = None
        # The sign-in is a token and the session cookie that goes with it.
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
                                                  urllib.request.HTTPSHandler(context=INSECURE))
        self.token = self.call('POST', '/api/auth/login', {'username': env('WUI_USER'), 'password': env('WUI_PASS')})['token']

    def call(self, method, path, body=None):
        req = urllib.request.Request(self.base + path, method=method, data=json.dumps(body).encode() if body is not None else None,
                                     headers={'Content-Type': 'application/json', **({'Authorization': 'Bearer ' + self.token} if self.token else {})})
        try:
            with self.opener.open(req, timeout=180) as r:
                raw = r.read()
        except urllib.error.HTTPError as e:
            raise RuntimeError(f'{method} {path}: {e.code} {e.read().decode(errors="replace")[:300]}')
        return json.loads(raw) if raw else None

    def nodes(self):
        return {n['name']: n for n in self.call('GET', '/api/nodes')}

    def customers(self):
        r = self.call('GET', '/api/clients')
        return r.get('items', []) if isinstance(r, dict) else r

    def subscription(self, client_id):
        """A customer's subscription, as a proxy app reads it: its links."""
        c = self.call('GET', f'/api/clients/{client_id}')
        s = self.call('GET', '/api/subscription')
        scheme = 'https' if s.get('certFile') else 'http'
        host = urllib.parse.urlparse(self.base).hostname
        raw = urllib.request.urlopen(urllib.request.Request(f"{scheme}://{host}:{s['port']}{s['path']}{c['subId']}",
                                                            headers={'User-Agent': 'v2rayNG/1.9'}), context=INSECURE, timeout=30).read()
        return [l for l in base64.b64decode(raw).decode().split() if '://' in l]


class XUI:
    """A 3x-ui panel: by token (XUI3_URL, XUI3_TOKEN) or signed in
    (XUI2_URL, XUI2_USER, XUI2_PASS)."""

    def __init__(self, url, token=None, user=None, password=None):
        self.root = url.rstrip('/') + '/'
        self.token = token
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
                                                  urllib.request.HTTPSHandler(context=INSECURE))
        if user:
            self.opener.open(urllib.request.Request(self.root + 'login', data=urllib.parse.urlencode({'username': user, 'password': password}).encode()), timeout=30)

    @classmethod
    def v3(cls):
        return cls(env('XUI3_URL'), token=env('XUI3_TOKEN'))

    @classmethod
    def v2(cls):
        return cls(env('XUI2_URL'), user=env('XUI2_USER'), password=env('XUI2_PASS'))

    def call(self, path, body=None, form=None):
        data = json.dumps(body).encode() if body is not None else urllib.parse.urlencode(form).encode() if form is not None else None
        headers = {'Content-Type': 'application/json'} if body is not None else {}
        if self.token:
            headers['Authorization'] = 'Bearer ' + self.token
        req = urllib.request.Request(self.root + 'panel/api/' + path, data=data, headers=headers, method='POST' if data is not None else 'GET')
        with self.opener.open(req, timeout=60) as r:
            return json.load(r)

    def inbounds(self):
        return self.call('inbounds/list')['obj']

    def clients(self):
        """Every client there, by (inbound id, email)."""
        out = {}
        for ib in self.inbounds():
            s = ib['settings']
            s = json.loads(s) if isinstance(s, str) else s
            for c in s.get('clients', []):
                out[(ib['id'], c['email'])] = c
        return out

    def xray_state(self):
        return self.call('server/status')['obj']['xray']


class Pasarguard:
    """A Pasarguard panel: PG_URL, PG_API_KEY."""

    def __init__(self):
        self.base = env('PG_URL').rstrip('/')
        self.key = env('PG_API_KEY')

    def call(self, path, method='GET', body=None):
        req = urllib.request.Request(self.base + path, method=method, data=json.dumps(body).encode() if body is not None else None,
                                     headers={'X-Api-Key': self.key, 'Content-Type': 'application/json'})
        return _json(req)


class Checks:
    """Checks counted, said as they pass or fail, and summed at the end."""

    def __init__(self):
        self.results = []

    def __call__(self, name, ok, detail=''):
        self.results.append(bool(ok))
        print(('PASS ' if ok else 'FAIL ') + name + (f' -- {detail}' if detail != '' else ''), flush=True)
        return ok

    def done(self):
        passed = sum(self.results)
        print(f'\n{passed} passed, {len(self.results) - passed} failed')
        sys.exit(0 if passed == len(self.results) else 1)


def _within(spans, port):
    """Whether port is in spans, written as 33002-33007,36001-36007."""
    port = int(port or 0)
    for span in spans.split(','):
        lo, _, hi = span.partition('-')
        if int(lo) <= port <= int(hi or lo):
            return True
    return False


def link_name(link):
    """The name a link carries: after # for most, in the JSON for vmess."""
    if link.startswith('vmess://'):
        try:
            return json.loads(base64.b64decode(link[8:] + '=' * (-len(link[8:]) % 4))).get('ps', '')
        except ValueError:
            return ''
    return urllib.parse.unquote(link.split('#', 1)[1]) if '#' in link else ''


class Dialer:
    """Links dialled through a local xray each, the way a customer's app
    would, to see that traffic passes. ECHO_URL answers /ip with the address
    it was reached from and serves /2m.bin."""

    def __init__(self, wui, port_base=24000):
        self.wui = wui
        self.port_base = port_base
        self.procs = []
        self.echo = env('ECHO_URL').rstrip('/')
        # Inbounds of a panel in docker are reached at its address there.
        self.rewrite = json.loads(os.environ.get('DIAL_REWRITE', '{}'))  # {"33002-33007": "172.17.0.2"}
        # A self-signed certificate pasted into the inbounds on these ports,
        # pinned there instead of trusted: {"33002-33007,36001-36007": "<sha256>"}.
        self.pins = json.loads(os.environ.get('DIAL_PINS', '{}'))

    def open(self, link):
        """A local SOCKS port carrying link, or None for a link xray cannot dial."""
        try:
            parsed = self.wui.call('POST', '/api/outbounds/parse', {'text': link})
        except RuntimeError:
            return None
        if not parsed.get('config'):
            return None
        ob = json.loads(parsed['config'])
        ob['tag'] = 'out'
        servers = [s for k in ('vnext', 'servers') for s in (ob.get('settings') or {}).get(k, [])] or [ob.get('settings') or {}]
        ts = (ob.get('streamSettings') or {}).get('tlsSettings')
        if ts is not None:
            ts.pop('allowInsecure', None)  # verified, or pinned below; never skipped
            for spans, pin in self.pins.items():
                if any(_within(spans, s.get('port')) for s in servers):
                    ts['pinnedPeerCertSha256'] = pin
        for srv in servers:
            for spans, addr in self.rewrite.items():
                if _within(spans, srv.get('port')):
                    srv['address'] = addr
        port = self.port_base + len(self.procs)
        path = f'/tmp/xray-nodes-dial-{port}.json'
        with open(path, 'w') as f:
            json.dump({'log': {'loglevel': 'warning'}, 'inbounds': [{'listen': '127.0.0.1', 'port': port, 'protocol': 'socks', 'settings': {'udp': True}}],
                       'outbounds': [ob]}, f)
        self.procs.append(subprocess.Popen([env('XRAY', '/usr/local/bin/xray'), 'run', '-c', path], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL))
        time.sleep(1)
        return port

    def passes(self, port):
        """The address the echo saw, and the bytes of 2 MB that came back."""
        curl = ['curl', '-s', '-m', '30', '-x', f'socks5h://127.0.0.1:{port}']
        ip = subprocess.run(curl + [self.echo + '/ip'], capture_output=True, text=True).stdout.strip()
        size = subprocess.run(curl + ['-o', '/dev/null', '-w', '%{size_download}', self.echo + '/2m.bin'], capture_output=True, text=True).stdout
        return ip, size

    def answers(self, port):
        """Whether the echo answers through port at all, quickly."""
        out = subprocess.run(['curl', '-s', '-m', '5', '-x', f'socks5h://127.0.0.1:{port}', self.echo + '/ip'], capture_output=True, text=True).stdout
        return bool(out.strip())

    def close(self):
        for p in self.procs:
            p.send_signal(signal.SIGTERM)
        self.procs = []
