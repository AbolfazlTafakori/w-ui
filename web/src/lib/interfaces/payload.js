// What the interface form sends to the server. The form holds fields that are
// its own (the AmneziaWG editor's Auto/Manual, the stealth transport's
// settings while it is plain UDP, the protocols not chosen), and the server
// refuses a field it does not know, so a request is built from the fields the
// server takes, named one by one, never from the form with a few taken out.
import { ocPayload, proven, wsOf, wsPayload } from './rows.js'

// A WireGuard tunnel's transport as the form asks for it: plain UDP, or a
// stealth one and its port, and anchor's, drift's, relay's or bare's name, path and
// decoy. AmneziaWG has only plain UDP.
export function wgTransportOf(input) {
  if (input.mode === 'amnezia' || !input.transport || input.transport === 'udp') return { transport: 'udp', transportPort: 0 }
  const out = { transport: input.transport, transportPort: Number(input.transportPort) || 0 }
  if (proven(input.transport) || input.transport === 'mirror') out.transportWs = wsPayload(input.ws)
  return out
}
export function wgTransportChanged(input, iface) {
  const want = wgTransportOf(input)
  if (want.transport !== (iface.stealth?.transport || 'udp') || want.transportPort !== (iface.stealth?.port || 0)) return true
  // An empty path or cookie keeps the one there is, so it is no change.
  if (!want.transportWs) return false
  const had = wsPayload(wsOf(iface.stealth))
  const kept = (k) => (k === 'path' || k === 'cookie') && want.transportWs[k] === ''
  return Object.keys(want.transportWs).some((k) => !kept(k) && want.transportWs[k] !== had[k])
}

// The advanced WireGuard overrides, as the server takes them: blanks become
// zeros, which it reads as "use the default". A zero keepalive and MTU and an
// empty AllowedIPs are exactly the defaults, so clearing a field restores them.
export function wgAdvancedForSend(a) {
  return {
    keepalive: Number(a?.keepalive) || 0,
    allowedIps: (a?.allowedIps || '').trim(),
    mtu: Number(a?.mtu) || 0,
    postUp: (a?.postUp || '').trim(),
    postDown: (a?.postDown || '').trim(),
  }
}
export function wgAdvancedNonEmpty(a) {
  const v = wgAdvancedForSend(a)
  return !!(v.keepalive || v.mtu || v.allowedIps || v.postUp || v.postDown)
}
export function wgAdvancedChanged(input, iface) {
  const want = wgAdvancedForSend(input.wgAdvanced)
  const had = wgAdvancedForSend(iface.wgAdvanced)
  return (
    want.keepalive !== had.keepalive ||
    want.allowedIps !== had.allowedIps ||
    want.mtu !== had.mtu ||
    want.postUp !== had.postUp ||
    want.postDown !== had.postDown
  )
}
// A new interface as the server's CreateInterfaceInput takes it.
export function createPayload(input) {
  const p = input.protocol
  return {
    name: input.name,
    protocol: p,
    listenPort: Number(input.listenPort),
    subnet: input.subnet,
    endpointHost: input.endpointHost,
    mtu: Number(input.mtu),
    dns: input.dns,
    natInterface: input.natInterface,
    mode: input.mode,
    nodeId: input.nodeId,
    enabled: input.enabled,
    // Each protocol's own, only on that protocol: the server refuses them
    // elsewhere rather than ignoring them.
    ...(p === 'openvpn' && input.transport ? { transport: input.transport } : {}),
    ...(p === 'l2tp' && input.psk ? { psk: input.psk } : {}),
    ...(p === 'l2tp' && input.l2tpPlain ? { l2tpPlain: true } : {}),
    ...(p === 'ikev2' ? { ikev2Modes: input.ikev2Modes } : {}),
    ...(p === 'openconnect' ? { openconnect: ocPayload(input.oc) } : {}),
    ...(p === 'wireguard' && wgTransportOf(input).transport !== 'udp' ? wgTransportOf(input) : {}),
    // The profile the form shows, as drawn for this server and perhaps
    // changed: what is created is what the operator saw.
    ...(p === 'wireguard' && input.mode === 'amnezia' ? (input.awg ? { awg: input.awg } : { awgLevel: input.awgLevel }) : {}),
    // Only when the operator set one: a bare tunnel keeps the defaults.
    ...(p === 'wireguard' && wgAdvancedNonEmpty(input.wgAdvanced) ? { wgAdvanced: wgAdvancedForSend(input.wgAdvanced) } : {}),
  }
}
