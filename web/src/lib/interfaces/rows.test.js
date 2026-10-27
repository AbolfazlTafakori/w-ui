import { describe, expect, it } from 'vitest'
import { exportable, nextSort, ocPayload, protoTag, rowItems, sortRows, totals, transportTag, wsOf, wsPayload } from './rows.js'

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

describe('wui-ws in a tunnel’s form', () => {
  const stealth = {
    transport: 'wui-ws', port: 8081, host: 'cdn.example.com', frontPort: 8443, path: '/media/7f3a9c2e1b',
    origin: 'http://127.0.0.1:8080', proxies: '198.51.100.0/24', clientHeader: 'CF-Connecting-IP', keyAt: 7,
  }

  it('reads its front, path and decoy, and nothing of another transport', () => {
    expect(wsOf(stealth)).toEqual({
      host: 'cdn.example.com', frontPort: 8443, path: '/media/7f3a9c2e1b',
      origin: 'http://127.0.0.1:8080', proxies: '198.51.100.0/24', clientHeader: 'CF-Connecting-IP',
    })
    expect(wsOf({ transport: 'wui-udp', port: 2096 })).toEqual({ host: '', frontPort: '', path: '', origin: '', proxies: '', clientHeader: '' })
    expect(wsOf(null).host).toBe('')
  })

  it('sends a port as a number and trims what was typed', () => {
    expect(wsPayload({ host: ' f.example ', frontPort: '', path: '', origin: '', proxies: ' 10.0.0.0/8 ', clientHeader: '' }))
      .toEqual({ host: 'f.example', frontPort: 0, path: '', origin: '', proxies: '10.0.0.0/8', clientHeader: '' })
  })

  it('travels in an export without its key', () => {
    const e = exportable({ name: 'wg0', protocol: 'wireguard', stealth: { ...stealth, key: 'secret' } })
    expect(e.transport).toBe('wui-ws')
    expect(e.transportPort).toBe(8081)
    expect(e.transportWs.path).toBe('/media/7f3a9c2e1b')
    expect(JSON.stringify(e)).not.toContain('secret')
    expect(transportTag({ protocol: 'wireguard', stealth })).toBe('UDP + wui-ws')
  })
})
