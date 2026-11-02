import { describe, expect, it } from 'vitest'
import { exportable, fronted, nextSort, ocPayload, proven, protoTag, rowItems, sortRows, totals, transportTag, wsOf, wsPayload } from './rows.js'

describe('a tunnel’s row', () => {
  it('says what it carries its traffic over', () => {
    expect(transportTag({ protocol: 'pptp' })).toBe('TCP')
    expect(transportTag({ protocol: 'openconnect', openconnect: { dtls: true } })).toBe('TCP + UDP')
    expect(transportTag({ protocol: 'openvpn', openvpn: { transport: 'tcp' } })).toBe('TCP')
    expect(transportTag({ protocol: 'wireguard' })).toBe('UDP')
    expect(transportTag({ protocol: 'xray', xui: { network: 'ws', security: 'tls' } })).toBe('WS · tls')
    expect(protoTag({ protocol: 'xray', xui: { kind: 'vless' } })).toBe('vless')
  })

  it('sorts up, down, then back as it came', () => {
    const rows = [{ id: 2, name: 'b' }, { id: 1, name: 'a' }, { id: 3, name: 'c' }]
    let s = nextSort({ key: '', dir: '' }, 'id')
    expect(sortRows(rows, s).map((r) => r.id)).toEqual([1, 2, 3])
    s = nextSort(s, 'id')
    expect(sortRows(rows, s).map((r) => r.id)).toEqual([3, 2, 1])
    s = nextSort(s, 'id')
    expect(sortRows(rows, s)).toBe(rows)
  })

  it('adds up the list', () => {
    expect(totals([{ usedBytes: 5, clients: 2 }, { usedBytes: 7 }])).toMatchObject({ used: 12, clients: 2, count: 2 })
  })

  it('offers on a 3x-ui inbound only what this panel can do to it', () => {
    const keys = (i) => rowItems(i).filter((m) => !m.divider).map((m) => m.key)
    expect(keys({ protocol: 'xray', clients: 3 })).toEqual(['urls', 'attachExisting', 'attachTo', 'detach', 'group', 'delAll'])
    expect(keys({ protocol: 'xray', clients: 0, xui: { gone: true } })).toContain('delete')
    expect(keys({ protocol: 'openvpn', clients: 0 })).toContain('profile')
  })

  it('exports what the create call takes, and nothing else', () => {
    const e = exportable({ name: 'l', protocol: 'l2tp', l2tp: { psk: 'k', allowPlain: true }, awg: { jc: 3 } })
    expect(e).toMatchObject({ psk: 'k', l2tpPlain: true })
    expect(e.awg).toBeUndefined()
    expect(ocPayload({ dtls: true, camouflageSecret: '  ' })).toEqual({ dtls: true, camouflage: false, usePanelCert: false })
  })
})

describe('drift in a tunnel’s form', () => {
  const stealth = {
    transport: 'drift', port: 8081, host: 'cdn.example.com', frontPort: 8443, path: '/media/7f3a9c2e1b',
    origin: 'http://127.0.0.1:8080', proxies: '198.51.100.0/24', clientHeader: 'CF-Connecting-IP', keyAt: 7,
  }

  it('reads its front, path and decoy, and nothing of another transport', () => {
    expect(wsOf(stealth)).toEqual({
      host: 'cdn.example.com', frontPort: 8443, path: '/media/7f3a9c2e1b',
      origin: 'http://127.0.0.1:8080', proxies: '198.51.100.0/24', clientHeader: 'CF-Connecting-IP', cookie: '',
      shape: 'get-poll', site: '', usePanelCert: false, proxyProtocol: false,
    })
    expect(wsOf({ transport: 'veil', port: 2096 })).toEqual({ host: '', frontPort: '', path: '', origin: '', proxies: '', clientHeader: '', cookie: '', shape: 'get-poll', site: '', usePanelCert: false, proxyProtocol: false })
    expect(wsOf(null).host).toBe('')
  })

  it('sends a port as a number and trims what was typed', () => {
    expect(wsPayload({ host: ' f.example ', frontPort: '', path: '', origin: '', proxies: ' 10.0.0.0/8 ', clientHeader: '' }))
      .toEqual({ host: 'f.example', frontPort: 0, path: '', origin: '', proxies: '10.0.0.0/8', clientHeader: '', cookie: '', shape: '', site: '', usePanelCert: false, proxyProtocol: false })
  })

  it('travels in an export without its key', () => {
    const e = exportable({ name: 'wg0', protocol: 'wireguard', stealth: { ...stealth, key: 'secret' } })
    expect(e.transport).toBe('drift')
    expect(e.transportPort).toBe(8081)
    expect(e.transportWs.path).toBe('/media/7f3a9c2e1b')
    expect(JSON.stringify(e)).not.toContain('secret')
    expect(transportTag({ protocol: 'wireguard', stealth })).toBe('UDP + drift')
  })
})

describe('relay in a tunnel’s form', () => {
  const stealth = {
    transport: 'relay', port: 8085, host: 'cdn.example.com', path: '/media/7f3a9c2e1b',
    origin: 'http://127.0.0.1:8080', proxies: '198.51.100.0/24', keyAt: 7,
  }

  it('is behind a front, with drift’s settings', () => {
    expect(fronted('relay')).toBe(true)
    expect(proven('relay')).toBe(true)
    expect(wsOf(stealth).host).toBe('cdn.example.com')
    expect(wsOf(stealth).cookie).toBe('')
  })

  it('travels in an export without its key', () => {
    const e = exportable({ name: 'wg0', protocol: 'wireguard', stealth: { ...stealth, key: 'secret', previousKey: 'older' } })
    expect(e.transport).toBe('relay')
    expect(e.transportPort).toBe(8085)
    expect(e.transportWs.path).toBe('/media/7f3a9c2e1b')
    expect(JSON.stringify(e)).not.toContain('secret')
    expect(JSON.stringify(e)).not.toContain('older')
    expect(transportTag({ protocol: 'wireguard', stealth })).toBe('UDP + relay')
  })
})

describe('bare in a tunnel’s form', () => {
  const stealth = {
    transport: 'bare', port: 8082, host: 'plain.example.com', path: '/static/a8d0c1e9f4',
    origin: 'http://127.0.0.1:8080', cookie: 'sess_2b', keyAt: 7,
  }

  it('reads its front, its decoy and its cookie', () => {
    expect(wsOf(stealth)).toMatchObject({ host: 'plain.example.com', path: '/static/a8d0c1e9f4', origin: 'http://127.0.0.1:8080', cookie: 'sess_2b' })
    expect(fronted('bare')).toBe(true)
    expect(fronted('veil')).toBe(false)
  })

  it('travels in an export with its cookie and without its keys', () => {
    const e = exportable({ name: 'wg0', protocol: 'wireguard', stealth: { ...stealth, key: 'secret', previousKey: 'older' } })
    expect(e.transport).toBe('bare')
    expect(e.transportWs.cookie).toBe('sess_2b')
    expect(JSON.stringify(e)).not.toContain('secret')
    expect(JSON.stringify(e)).not.toContain('older')
    expect(transportTag({ protocol: 'wireguard', stealth })).toBe('UDP + bare')
  })
})

describe('anchor in a tunnel’s form', () => {
  const stealth = {
    transport: 'anchor', port: 443, host: 'edge.example.com', path: '/assets/c4e1a9b7f2',
    origin: 'http://127.0.0.1:8080', usePanelCert: true, proxyProtocol: true, certSource: 'own', cert: 'CERT', keyAt: 7,
  }

  it('reads its name, path, decoy and certificate choice, and is no front', () => {
    expect(wsOf(stealth)).toMatchObject({ host: 'edge.example.com', path: '/assets/c4e1a9b7f2', origin: 'http://127.0.0.1:8080', usePanelCert: true, proxyProtocol: true })
    expect(proven('anchor')).toBe(true)
    expect(proven('drift')).toBe(true)
    expect(proven('veil')).toBe(false)
    expect(fronted('anchor')).toBe(false)
  })

  it('travels in an export without its keys', () => {
    const e = exportable({ name: 'wg0', protocol: 'wireguard', stealth: { ...stealth, key: 'secret', certKey: 'certsecret' } })
    expect(e.transport).toBe('anchor')
    expect(e.transportWs).toMatchObject({ host: 'edge.example.com', usePanelCert: true, proxyProtocol: true })
    expect(JSON.stringify(e)).not.toContain('secret')
    expect(transportTag({ protocol: 'wireguard', stealth })).toBe('UDP + anchor')
  })
})

describe('mirror in a tunnel’s form', () => {
  const stealth = { transport: 'mirror', port: 8443, site: 'www.wikipedia.org:443', nodeKey: 'NODEKEY', keyAt: 7 }

  it('reads its borrowed site, and carries it in an export without its key', () => {
    expect(wsOf(stealth)).toMatchObject({ site: 'www.wikipedia.org:443', host: '', path: '' })
    expect(proven('mirror')).toBe(false)
    expect(fronted('mirror')).toBe(false)
    const e = exportable({ name: 'wg0', protocol: 'wireguard', stealth })
    expect(e.transport).toBe('mirror')
    expect(e.transportWs.site).toBe('www.wikipedia.org:443')
    expect(JSON.stringify(e)).not.toContain('NODEKEY')
    expect(transportTag({ protocol: 'wireguard', stealth })).toBe('UDP + mirror')
  })
})
