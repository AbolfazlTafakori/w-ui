import { describe, expect, it } from 'vitest'
import { createPayload, importPayload } from './payload.js'
import { exportable } from './rows.js'

// The json names of service.CreateInterfaceInput. The server refuses any
// other, so a form field of the UI's own must never reach it.
const SERVER = new Set(['xray', 'name', 'protocol', 'listenPort', 'subnet', 'endpointHost', 'mtu', 'dns', 'natInterface', 'mode',
  'nodeId', 'transport', 'enabled', 'psk', 'l2tpPlain', 'ikev2Modes', 'openconnect', 'awg', 'awgLevel', 'wgAdvanced',
  'transportPort', 'transportWs'])

// The form as InterfaceForm.vue opens it for a new tunnel, every field set.
const form = (over) => ({
  name: 't', protocol: 'wireguard', listenPort: '51820', subnet: '10.8.0.0/24', endpointHost: 'vpn.example.com', mtu: '1420',
  dns: '1.1.1.1', natInterface: 'eth0', mode: 'standard', nodeId: 0, transport: 'udp', transportPort: '',
  ws: { host: '', path: '', decoy: '' }, psk: 'k', l2tpPlain: true, ikev2Modes: ['eap'],
  oc: { dtls: true, camouflage: true, camouflageSecret: '', usePanelCert: true },
  awg: null, awgLevel: 'modern', awgMode: 'auto',
  wgAdvanced: { keepalive: '25', allowedIps: '', mtu: '', postUp: '', postDown: '' }, enabled: true, ...over,
})

const cases = {
  wireguard: {},
  'wireguard over a stealth transport': { transport: 'anchor', transportPort: '443' },
  'amneziawg, auto': { mode: 'amnezia', awgMode: 'auto' },
  'amneziawg, manual': { mode: 'amnezia', awgMode: 'manual', awg: { jc: 4 } },
  'openvpn udp': { protocol: 'openvpn', mode: '' },
  'openvpn tcp': { protocol: 'openvpn', transport: 'tcp', mode: '' },
  l2tp: { protocol: 'l2tp' },
  ikev2: { protocol: 'ikev2' },
  openconnect: { protocol: 'openconnect' },
  pptp: { protocol: 'pptp' },
}

describe('a new interface, as sent', () => {
  for (const [name, over] of Object.entries(cases)) {
    it(`carries only fields the server knows: ${name}`, () => {
      const unknown = Object.keys(createPayload(form(over))).filter((k) => !SERVER.has(k))
      expect(unknown).toEqual([])
    })
  }

  it('keeps each protocol’s own fields on that protocol', () => {
    expect(createPayload(form({ protocol: 'openvpn', transport: 'tcp' }))).toMatchObject({ transport: 'tcp' })
    expect(createPayload(form({ protocol: 'openvpn' }))).not.toHaveProperty('psk')
    expect(createPayload(form({ protocol: 'l2tp' }))).toMatchObject({ psk: 'k', l2tpPlain: true })
    expect(createPayload(form({ mode: 'amnezia' }))).toMatchObject({ awgLevel: 'modern' })
    expect(createPayload(form({ mode: 'amnezia', awg: { jc: 4 } }))).toMatchObject({ awg: { jc: 4 } })
    expect(createPayload(form({}))).toMatchObject({ listenPort: 51820, mtu: 1420, wgAdvanced: { keepalive: 25 } })
  })
})

// A tunnel as the interface list returns it, keys already taken out.
const row = (over) => ({
  id: 7, name: 'wg-s', protocol: 'wireguard', enabled: true, listenPort: 51820, subnet: '10.8.0.0/24',
  endpointHost: 'vpn.example.com', mtu: 1420, dns: '1.1.1.1', natInterface: 'eth0', mode: 'standard', ...over,
})

describe('an exported tunnel, imported', () => {
  const roundTrip = (r) => importPayload(JSON.parse(JSON.stringify(exportable(r))))

  it('keeps a WireGuard tunnel’s stealth transport, its port and its settings', () => {
    const got = roundTrip(row({ stealth: { transport: 'anchor', port: 443, host: 'cdn.example.com', path: '/x' } }))
    expect(got).toMatchObject({ transport: 'anchor', transportPort: 443, transportWs: { host: 'cdn.example.com', path: '/x' } })
  })

  it('keeps IKEv2’s ways in and OpenConnect’s settings', () => {
    expect(roundTrip(row({ protocol: 'ikev2', mode: '', ikev2: { mschapv2: true, psk: false, rsa: true, eapTls: false } })))
      .toMatchObject({ ikev2Modes: ['mschapv2', 'rsa'] })
    expect(roundTrip(row({ protocol: 'openconnect', mode: '', openconnect: { dtls: false, camouflage: true, camouflageSecret: 'Sec', usePanelCert: true } })))
      .toMatchObject({ openconnect: { dtls: false, camouflage: true, camouflageSecret: 'Sec', usePanelCert: true } })
  })

  it('keeps an L2TP key, an OpenVPN transport and WireGuard’s overrides', () => {
    expect(roundTrip(row({ protocol: 'l2tp', mode: '', l2tp: { psk: 'K', allowPlain: true } }))).toMatchObject({ psk: 'K', l2tpPlain: true })
    expect(roundTrip(row({ protocol: 'openvpn', mode: '', openvpn: { transport: 'tcp' } }))).toMatchObject({ transport: 'tcp' })
    expect(roundTrip(row({ wgAdvanced: { keepalive: 25 } }))).toMatchObject({ wgAdvanced: { keepalive: 25 } })
  })

  it('reads an export from before it took this shape', () => {
    expect(importPayload({ name: 'o', protocol: 'openvpn', listenPort: 1194, openvpn: { transport: 'tcp', caCert: 'x' } }))
      .toEqual({ name: 'o', protocol: 'openvpn', listenPort: 1194, transport: 'tcp' })
  })

  for (const over of [{}, { stealth: { transport: 'drift', port: 8443 } }, { mode: 'amnezia', awg: { jc: 3 } },
    { protocol: 'ikev2', ikev2: { psk: true } }, { protocol: 'openconnect', openconnect: { dtls: true } },
    { protocol: 'l2tp', l2tp: { psk: 'K' } }, { protocol: 'openvpn', openvpn: { transport: 'udp' } }]) {
    it(`carries only fields the server knows: ${over.protocol || 'wireguard'} ${over.mode || ''}`, () => {
      const unknown = Object.keys(roundTrip(row(over))).filter((k) => !SERVER.has(k))
      expect(unknown).toEqual([])
    })
  }
})
