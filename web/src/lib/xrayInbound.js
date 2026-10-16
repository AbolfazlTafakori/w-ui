// An inbound for a 3x-ui or Pasarguard node, as their own forms make one.
//
// The form keeps a flat state (newInboundState); buildInbound turns it into
// what that panel's xray takes: the protocol's settings, streamSettings for
// the transport and security, sniffing, and on 3x-ui the inbound's own
// allowance and expiry, on Pasarguard the host its users' links are written
// for. What a server does not have is never offered: the kinds come from the
// node (GET /api/nodes/{id}/inbound-kinds).

export const XRAY_NETWORKS = {
  tcp: 'TCP (RAW)',
  ws: 'WebSocket',
  grpc: 'gRPC',
  httpupgrade: 'HTTPUpgrade',
  xhttp: 'XHTTP',
  kcp: 'mKCP',
}

export const SS_METHODS = [
  '2022-blake3-aes-256-gcm',
  '2022-blake3-aes-128-gcm',
  '2022-blake3-chacha20-poly1305',
  'aes-256-gcm',
  'aes-128-gcm',
  'chacha20-ietf-poly1305',
  'xchacha20-ietf-poly1305',
]

export const FINGERPRINTS = ['chrome', 'firefox', 'safari', 'ios', 'android', 'edge', '360', 'qq', 'random', 'randomized']
export const ALPNS = ['h3', 'h2', 'http/1.1']
export const KCP_HEADERS = ['none', 'srtp', 'utp', 'wechat-video', 'dtls', 'wireguard', 'dns']
export const XHTTP_MODES = ['auto', 'packet-up', 'stream-up', 'stream-one']
export const TRAFFIC_RESETS = ['never', 'hourly', 'daily', 'weekly', 'monthly']

// The port each protocol is usually given, as the panels' forms start.
export const DEFAULT_PORTS = { vless: 443, vmess: 8080, trojan: 443, shadowsocks: 8388, hysteria: 443, tuic: 443, mtproto: 443, wireguard: 51820, amneziawg: 51821, masque: 443, mixed: 1080, socks: 1080, http: 8080, tunnel: 10000, 'dokodemo-door': 10000 }

export function newInboundState() {
  return {
    protocol: 'vless',
    listen: '',
    // 3x-ui: the inbound's own allowance, expiry and reset.
    totalGB: 0,
    expiry: '',
    trafficReset: 'never',
    trafficResetDay: 1,
    // Pasarguard: the host its users' links are written for.
    host: { address: '', port: '', sni: '', host: '', path: '', fingerprint: '', alpn: [] },
    vless: { decryption: 'none', encryption: 'none', fallbacks: [] },
    trojan: { fallbacks: [] },
    ss: { method: '2022-blake3-aes-256-gcm', password: '', network: 'tcp,udp', ivCheck: false },
    hy: { congestion: '', up: '', down: '', udpIdleTimeout: 60, obfs: '' },
    mq: { path: '/.well-known/masque/ip/*/*/', host: '' },
    tuic: { certificate: '', private_key: '', congestion_control: 'bbr', alpn: ['h3'], udp_relay_mode: 'native', zero_rtt_handshake: true, max_idle_time: 15, authentication_timeout: 3, max_udp_relay_packet_size: 1500, sni: '' },
    mt: { fakeTlsDomain: 'www.cloudflare.com', preferIp: '', throttleMaxConnections: '', pendingHandshakesMaxPerIp: '', frontIp: '', frontPort: '', frontProxyProtocol: false, proxyProtocolListener: false, publicIpv4: '', publicIpv6: '' },
    wg: { secretKey: '', publicKey: '', mtu: 1420, dns: '', subnetIp: '10.0.0.0', subnetCidr: 24, address: '10.0.0.1/24', noKernelTun: false },
    awg: { privateKey: '', publicKey: '', subnetIp: '10.8.1.0', subnetCidr: 24, mtu: '', primaryDns: '8.8.8.8', secondaryDns: '8.8.4.4', externalInterface: '', ipv6Enabled: false, ipv6Subnet: '', routeThroughXray: false, jc: 5, jmin: 10, jmax: 50, s1: 30, s2: 45, s3: 10, s4: 5, h1: '', h2: '', h3: '', h4: '', i1: '', i2: '', i3: '', i4: '', i5: '', randomTrailers: false, disableCookies: false },
    masque: { address: '10.14.0.1/24, fd14::1/64', mtu: '' },
    mixed: { auth: 'password', accounts: '', udp: false, ip: '127.0.0.1' },
    http: { accounts: '', allowTransparent: false },
    tunnel: { address: '', port: '', network: 'tcp,udp', followRedirect: false },
    network: 'tcp',
    security: 'none',
    tcp: { acceptProxyProtocol: false, header: 'none', paths: '/', hosts: '' },
    ws: { path: '/', host: '', headers: '', heartbeatPeriod: '', acceptProxyProtocol: false },
    grpc: { serviceName: '', authority: '', multiMode: false },
    hu: { path: '/', host: '', headers: '', acceptProxyProtocol: false },
    xhttp: { path: '/', host: '', mode: 'auto', xPaddingBytes: '100-1000', noSSEHeader: false, scMaxEachPostBytes: '', scMaxBufferedPosts: '', scStreamUpServerSecs: '' },
    kcp: { mtu: 1350, tti: 20, uplinkCapacity: 5, downlinkCapacity: 20, congestion: false, readBufferSize: 1, writeBufferSize: 1, header: 'none', seed: '' },
    tls: { serverName: '', alpn: ['h2', 'http/1.1'], minVersion: '1.2', maxVersion: '1.3', cipherSuites: '', rejectUnknownSni: false, certMode: 'file', certificateFile: '', keyFile: '', certificate: '', key: '', fingerprint: 'chrome', allowInsecure: false, echServerKeys: '' },
    reality: { show: false, xver: 0, target: 'www.speedtest.net:443', serverNames: 'www.speedtest.net', privateKey: '', publicKey: '', shortIds: '', maxTimeDiff: 0, spiderX: '/', fingerprint: 'chrome', mldsa65Seed: '' },
    sniffing: { enabled: true, destOverride: ['http', 'tls', 'quic'], metadataOnly: false, routeOnly: false },
    advSettings: '',
    advStream: '',
  }
}

const list = (s) => String(s || '').split(/[\n,]/).map((v) => v.trim()).filter(Boolean)
const num = (v) => (v === '' || v === null || v === undefined ? undefined : Number(v))
const lines = (s) => String(s || '').split('\n').map((v) => v.replace(/\r$/, '')).filter((v) => v.trim() !== '')

// headers reads "Name: value" lines into the object xray takes.
function headers(text) {
  const out = {}
  for (const l of lines(text)) {
    const i = l.indexOf(':')
    if (i > 0) out[l.slice(0, i).trim()] = l.slice(i + 1).trim()
  }
  return out
}

function accounts(text) {
  return lines(text).map((l) => {
    const i = l.indexOf(':')
    return i > 0 ? { user: l.slice(0, i).trim(), pass: l.slice(i + 1).trim() } : { user: l.trim(), pass: '' }
  }).filter((a) => a.user)
}

function fallbacks(rows) {
  return rows.filter((f) => String(f.dest).trim() !== '').map((f) => {
    const out = { dest: /^\d+$/.test(String(f.dest).trim()) ? Number(f.dest) : String(f.dest).trim() }
    if (f.name) out.name = f.name
    if (f.alpn) out.alpn = f.alpn
    if (f.path) out.path = f.path
    if (Number(f.xver)) out.xver = Number(f.xver)
    return out
  })
}

// compact drops the keys left empty, as both panels' forms do.
function compact(o) {
  const out = {}
  for (const [k, v] of Object.entries(o)) {
    if (v === undefined || v === '' || (Array.isArray(v) && v.length === 0 && k !== 'clients' && k !== 'peers')) continue
    out[k] = v
  }
  return out
}

// merge lays an advanced JSON object over what the form built.
function merge(base, extra) {
  for (const [k, v] of Object.entries(extra || {})) {
    if (v && typeof v === 'object' && !Array.isArray(v) && base[k] && typeof base[k] === 'object' && !Array.isArray(base[k])) merge(base[k], v)
    else base[k] = v
  }
  return base
}

export function parseAdvanced(text) {
  const t = String(text || '').trim()
  if (!t) return {}
  const v = JSON.parse(t)
  if (!v || typeof v !== 'object' || Array.isArray(v)) throw new Error('an object')
  return v
}

// settingsFor is the protocol's settings object.
export function settingsFor(x, panel) {
  switch (x.protocol) {
    case 'vless':
      return compact({ clients: [], decryption: x.vless.decryption || 'none', ...(panel === 'xui' ? { encryption: x.vless.encryption || 'none' } : {}), fallbacks: fallbacks(x.vless.fallbacks) })
    case 'vmess':
      return { clients: [] }
    case 'trojan':
      return compact({ clients: [], fallbacks: fallbacks(x.trojan.fallbacks) })
    case 'shadowsocks':
      return compact({ clients: [], method: x.ss.method, password: x.ss.password, network: x.ss.network, ivCheck: x.ss.ivCheck || undefined })
    case 'hysteria':
      return { version: 2, clients: [] }
    case 'tuic':
      return {
        clients: [],
        server: compact({
          certificate: x.tuic.certificate, private_key: x.tuic.private_key, congestion_control: x.tuic.congestion_control,
          alpn: x.tuic.alpn, udp_relay_mode: x.tuic.udp_relay_mode, zero_rtt_handshake: x.tuic.zero_rtt_handshake,
          max_idle_time: num(x.tuic.max_idle_time), authentication_timeout: num(x.tuic.authentication_timeout),
          max_udp_relay_packet_size: num(x.tuic.max_udp_relay_packet_size), sni: x.tuic.sni,
        }),
      }
    case 'mtproto': {
      const m = x.mt
      const front = compact({ ip: m.frontIp, port: num(m.frontPort), proxyProtocol: m.frontProxyProtocol || undefined })
      return compact({
        clients: [], fakeTlsDomain: m.fakeTlsDomain, preferIp: m.preferIp,
        throttleMaxConnections: num(m.throttleMaxConnections), pendingHandshakesMaxPerIp: num(m.pendingHandshakesMaxPerIp),
        domainFronting: Object.keys(front).length ? front : undefined, proxyProtocolListener: m.proxyProtocolListener || undefined,
        publicIpv4: m.publicIpv4, publicIpv6: m.publicIpv6,
      })
    }
    case 'wireguard':
      if (panel === 'pasarguard') {
        return compact({ secretKey: x.wg.secretKey, address: list(x.wg.address), peers: [], mtu: num(x.wg.mtu), noKernelTun: x.wg.noKernelTun })
      }
      return compact({ secretKey: x.wg.secretKey, mtu: num(x.wg.mtu), dns: x.wg.dns, peers: [], clients: [], noKernelTun: x.wg.noKernelTun, subnetIp: x.wg.subnetIp, subnetCidr: num(x.wg.subnetCidr) })
    case 'amneziawg': {
      const a = x.awg
      const server = { privateKey: a.privateKey, publicKey: a.publicKey, subnetIp: a.subnetIp, subnetCidr: num(a.subnetCidr), mtu: num(a.mtu), primaryDns: a.primaryDns, secondaryDns: a.secondaryDns, externalInterface: a.externalInterface, ipv6Enabled: a.ipv6Enabled, ipv6Subnet: a.ipv6Subnet, routeThroughXray: a.routeThroughXray, randomTrailers: a.randomTrailers, disableCookies: a.disableCookies }
      for (const k of ['jc', 'jmin', 'jmax', 's1', 's2', 's3', 's4']) server[k] = num(a[k])
      for (const k of ['h1', 'h2', 'h3', 'h4', 'i1', 'i2', 'i3', 'i4', 'i5']) server[k] = a[k]
      return { server: compact(server), clients: [] }
    }
    case 'masque':
      return compact({ clients: [], address: list(x.masque.address), mtu: num(x.masque.mtu) })
    case 'mixed':
    case 'socks':
      return compact({ auth: x.mixed.auth, accounts: x.mixed.auth === 'password' ? accounts(x.mixed.accounts) : undefined, udp: x.mixed.udp, ip: x.mixed.ip })
    case 'http':
      return compact({ accounts: accounts(x.http.accounts), allowTransparent: x.http.allowTransparent })
    case 'tunnel':
    case 'dokodemo-door': {
      const t = x.tunnel
      if (x.protocol === 'tunnel') return compact({ rewriteAddress: t.address, rewritePort: num(t.port), allowedNetwork: t.network, followRedirect: t.followRedirect })
      return compact({ address: t.address, port: num(t.port), network: t.network, followRedirect: t.followRedirect })
    }
  }
  return {}
}

// streamFor is streamSettings: the transport and its settings, the
// security and its settings.
export function streamFor(x, panel) {
  if (x.protocol === 'masque') {
    return { network: 'masque', security: 'tls', masqueSettings: compact({ path: x.mq.path || '/.well-known/masque/ip/*/*/', host: x.mq.host }), tlsSettings: tlsFor(x, panel, ['h3']) }
  }
  if (x.protocol === 'hysteria') {
    const hy = compact({ version: 2, congestion: x.hy.congestion, up: x.hy.up, down: x.hy.down, udpIdleTimeout: num(x.hy.udpIdleTimeout) })
    const st = { network: 'hysteria', security: 'tls', tlsSettings: tlsFor(x, panel, ['h3']), hysteriaSettings: hy }
    if (x.hy.obfs) st.finalmask = { udp: [{ type: 'salamander', settings: { password: x.hy.obfs } }] }
    return st
  }
  const st = { network: x.network, security: x.security }
  switch (x.network) {
    case 'tcp': {
      const tcp = { acceptProxyProtocol: x.tcp.acceptProxyProtocol || undefined, header: { type: x.tcp.header } }
      if (x.tcp.header === 'http') {
        tcp.header.request = { version: '1.1', method: 'GET', path: list(x.tcp.paths).length ? list(x.tcp.paths) : ['/'], headers: list(x.tcp.hosts).length ? { Host: list(x.tcp.hosts) } : {} }
        tcp.header.response = { version: '1.1', status: '200', reason: 'OK', headers: {} }
      }
      st.tcpSettings = compact(tcp)
      break
    }
    case 'ws':
      st.wsSettings = compact({ path: x.ws.path || '/', host: x.ws.host, headers: Object.keys(headers(x.ws.headers)).length ? headers(x.ws.headers) : undefined, heartbeatPeriod: num(x.ws.heartbeatPeriod), acceptProxyProtocol: x.ws.acceptProxyProtocol || undefined })
      break
    case 'grpc':
      st.grpcSettings = compact({ serviceName: x.grpc.serviceName, authority: x.grpc.authority, multiMode: x.grpc.multiMode || undefined })
      break
    case 'httpupgrade':
      st.httpupgradeSettings = compact({ path: x.hu.path || '/', host: x.hu.host, headers: Object.keys(headers(x.hu.headers)).length ? headers(x.hu.headers) : undefined, acceptProxyProtocol: x.hu.acceptProxyProtocol || undefined })
      break
    case 'xhttp':
      st.xhttpSettings = compact({ path: x.xhttp.path || '/', host: x.xhttp.host, mode: x.xhttp.mode, xPaddingBytes: x.xhttp.xPaddingBytes, noSSEHeader: x.xhttp.noSSEHeader || undefined, scMaxEachPostBytes: x.xhttp.scMaxEachPostBytes, scMaxBufferedPosts: num(x.xhttp.scMaxBufferedPosts), scStreamUpServerSecs: x.xhttp.scStreamUpServerSecs })
      break
    case 'kcp':
      st.kcpSettings = compact({ mtu: num(x.kcp.mtu), tti: num(x.kcp.tti), uplinkCapacity: num(x.kcp.uplinkCapacity), downlinkCapacity: num(x.kcp.downlinkCapacity), congestion: x.kcp.congestion, readBufferSize: num(x.kcp.readBufferSize), writeBufferSize: num(x.kcp.writeBufferSize), header: { type: x.kcp.header }, seed: x.kcp.seed })
      break
  }
  if (x.security === 'tls') st.tlsSettings = tlsFor(x, panel)
  if (x.security === 'reality') st.realitySettings = realityFor(x, panel)
  return st
}

function tlsFor(x, panel, alpnDefault) {
  const t = x.tls
  const cert = t.certMode === 'file'
    ? { certificateFile: t.certificateFile.trim(), keyFile: t.keyFile.trim() }
    : { certificate: lines(t.certificate), key: lines(t.key) }
  const out = compact({
    // Over QUIC (Hysteria, MASQUE) it is h3 and nothing else.
    serverName: t.serverName, alpn: alpnDefault || (t.alpn && t.alpn.length ? t.alpn : undefined),
    minVersion: t.minVersion, maxVersion: t.maxVersion, cipherSuites: t.cipherSuites,
    rejectUnknownSni: t.rejectUnknownSni || undefined, echServerKeys: t.echServerKeys,
    certificates: [{ ...cert, oneTimeLoading: false, usage: 'encipherment', buildChain: false }],
  })
  // What 3x-ui writes its clients' links from.
  if (panel === 'xui') out.settings = { allowInsecure: !!t.allowInsecure, fingerprint: t.fingerprint || 'chrome' }
  return out
}

function realityFor(x, panel) {
  const r = x.reality
  // target is xray's name for it now, dest the one before; both are written
  // so an older xray on that server reads it too.
  const out = compact({
    show: r.show, xver: Number(r.xver) || 0, target: r.target, dest: r.target,
    serverNames: list(r.serverNames), privateKey: r.privateKey, shortIds: list(r.shortIds).length ? list(r.shortIds) : [''],
    maxTimeDiff: Number(r.maxTimeDiff) || 0, mldsa65Seed: r.mldsa65Seed,
  })
  if (panel === 'xui') out.settings = compact({ publicKey: r.publicKey, fingerprint: r.fingerprint || 'chrome', serverName: '', spiderX: r.spiderX || '/' })
  return out
}

// buildInbound is the body of POST /api/interfaces for the inbound. An
// advanced JSON object, if given, is laid over what the form built.
export function buildInbound(x, kind, panel) {
  const settings = merge(settingsFor(x, panel), parseAdvanced(x.advSettings))
  const out = { protocol: x.protocol, listen: x.listen.trim(), settings }
  // 3x-ui gives a WireGuard or tunnel inbound a stream with no transport.
  if (panel === 'xui' && (x.protocol === 'wireguard' || x.protocol === 'tunnel')) out.streamSettings = { security: 'none' }
  if (kind?.stream) {
    out.streamSettings = merge(streamFor(x, panel), parseAdvanced(x.advStream))
    out.sniffing = { enabled: !!x.sniffing.enabled, destOverride: x.sniffing.destOverride, metadataOnly: !!x.sniffing.metadataOnly, routeOnly: !!x.sniffing.routeOnly }
  }
  if (panel === 'xui') {
    out.total = Math.round((Number(x.totalGB) || 0) * 1024 * 1024 * 1024)
    out.expiryTime = x.expiry ? new Date(x.expiry).getTime() : 0
    out.trafficReset = x.trafficReset
    out.trafficResetDay = Number(x.trafficResetDay) || 1
  }
  if (panel === 'pasarguard') {
    const h = x.host
    out.host = compact({ address: list(h.address), port: num(h.port), sni: list(h.sni), host: list(h.host), path: h.path, fingerprint: h.fingerprint, alpn: h.alpn })
  }
  return out
}
