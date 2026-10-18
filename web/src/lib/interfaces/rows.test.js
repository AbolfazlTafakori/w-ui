import { describe, expect, it } from 'vitest'
import { exportable, nextSort, ocPayload, protoTag, rowItems, sortRows, totals, transportTag } from './rows.js'

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
